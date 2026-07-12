package state

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestStateStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.PutAdminSettings(AdminSettings{
		TorEnabled:     true,
		TorMode:        "dual",
		TorAutostart:   true,
		OnionAddress:   "exampleonion.onion",
		TorStatus:      "configured",
		TorControlAddr: "127.0.0.1:9051",
	}); err != nil {
		t.Fatal(err)
	}

	settings, err := st.GetAdminSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.TorEnabled || settings.TorMode != "dual" || settings.OnionAddress != "exampleonion.onion" {
		t.Fatalf("unexpected settings: %#v", settings)
	}
	if settings.AdminSessionEpoch != 0 {
		t.Fatalf("expected zero admin session epoch by default, got %#v", settings)
	}

	if bumped, err := st.BumpAdminSessionEpoch(); err != nil {
		t.Fatal(err)
	} else if bumped != 1 {
		t.Fatalf("unexpected bumped epoch: %d", bumped)
	}
	settings, err = st.GetAdminSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.AdminSessionEpoch != 1 {
		t.Fatalf("expected persisted admin session epoch, got %#v", settings)
	}

	if err := st.PutAdminSettings(AdminSettings{
		TorEnabled:        true,
		TorMode:           "dual",
		TorAutostart:      true,
		OnionAddress:      "exampleonion.onion",
		TorStatus:         "configured",
		TorControlAddr:    "127.0.0.1:9051",
		AdminSessionEpoch: 7,
	}); err != nil {
		t.Fatal(err)
	}
	settings, err = st.GetAdminSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.AdminSessionEpoch != 7 {
		t.Fatalf("expected explicit epoch to persist, got %#v", settings)
	}

	if err := st.CreateImportSource("Main feed", "http", "https://example.com/feed.json", true); err != nil {
		t.Fatal(err)
	}
	sources, err := st.ImportSources()
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Name != "Main feed" || !sources[0].Enabled {
		t.Fatalf("unexpected import sources: %#v", sources)
	}

	if err := st.CreateImportSource("Second feed", "file", "/tmp/second.json", false); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateImportRun(9999, "queued", "invalid source", "", ""); err == nil {
		t.Fatal("expected foreign-key constraint to reject an import run without a source")
	}
	pagedSources, err := st.ImportSourcesOffset(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pagedSources) != 1 || pagedSources[0].Name != "Second feed" {
		t.Fatalf("unexpected paged sources: %#v", pagedSources)
	}
	sourceCount, err := st.ImportSourceTotals()
	if err != nil {
		t.Fatal(err)
	}
	if sourceCount != 2 {
		t.Fatalf("unexpected source totals: %d", sourceCount)
	}

	if err := st.CreateImportRun(sources[0].ID, "queued", "created for test", "abc123", "approved/ref"); err != nil {
		t.Fatal(err)
	}
	runs, err := st.ImportRuns(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != "queued" || runs[0].ApprovedRef != "approved/ref" {
		t.Fatalf("unexpected import runs: %#v", runs)
	}
	runCount, err := st.ImportRunTotals()
	if err != nil {
		t.Fatal(err)
	}
	if runCount != 1 {
		t.Fatalf("unexpected run totals: %d", runCount)
	}

	if err := st.CreateImportManifest("Manifest One", "owner", "/tmp", "abc", "abc", 1024); err != nil {
		t.Fatal(err)
	}
	manifests, err := st.ImportManifests(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 1 || manifests[0].Name != "Manifest One" || manifests[0].ApprovedBy != "owner" {
		t.Fatalf("unexpected import manifests: %#v", manifests)
	}
	filteredManifests, err := st.ImportManifestsSearchOffset("one", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(filteredManifests) != 1 || filteredManifests[0].Name != "Manifest One" {
		t.Fatalf("unexpected filtered import manifests: %#v", filteredManifests)
	}
	manifest, err := st.ImportManifest(manifests[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "Manifest One" || manifest.BaseDir != "/tmp" || manifest.TotalBytes != 1024 {
		t.Fatalf("unexpected manifest detail: %#v", manifest)
	}
	if err := st.DeleteImportManifest(manifests[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportManifest(manifests[0].ID); err == nil {
		t.Fatal("expected deleted manifest lookup to fail")
	}
	if err := st.CreateImportManifest("Second Manifest", "owner", "/tmp", "def", "def", 2048); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordValidatedManifest(sources[0].ID, "queued", "recorded", "approved/ref", "Atomic Manifest", "owner", "/tmp", "ghi", "ghi", 512); err != nil {
		t.Fatal(err)
	}
	manifests, err = st.ImportManifests(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 2 || manifests[0].Name != "Atomic Manifest" {
		t.Fatalf("expected atomically recorded manifest, got %#v", manifests)
	}
	runs, err = st.ImportRuns(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].ApprovedRef != "approved/ref" {
		t.Fatalf("expected atomically recorded run, got %#v", runs)
	}
	count, totalBytes, err := st.ImportManifestTotals()
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || totalBytes != 2560 {
		t.Fatalf("unexpected manifest totals: count=%d bytes=%d", count, totalBytes)
	}

	if err := st.FinishImportRun(runs[0].ID, "finished", "done"); err != nil {
		t.Fatal(err)
	}
	runs, err = st.ImportRuns(10)
	if err != nil {
		t.Fatal(err)
	}
	if runs[0].Status != "finished" || runs[0].Message != "done" {
		t.Fatalf("unexpected finished run: %#v", runs[0])
	}

	if err := st.Audit("import_manifest_preview", "preview details"); err != nil {
		t.Fatal(err)
	}
	audits, err := st.AuditEntries(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].Action != "import_manifest_preview" || audits[0].Details != "preview details" {
		t.Fatalf("unexpected audits: %#v", audits)
	}
	if err := st.Audit("audit_export", "export details"); err != nil {
		t.Fatal(err)
	}
	auditCount, err := st.AuditTotals()
	if err != nil {
		t.Fatal(err)
	}
	if auditCount != 2 {
		t.Fatalf("unexpected audit totals: %d", auditCount)
	}

	if _, err := st.LatestAuditByAction("admin_login"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected missing latest login audit lookup to return sql.ErrNoRows, got %v", err)
	}
	if err := st.Audit("admin_login", "session established"); err != nil {
		t.Fatal(err)
	}
	lastLogin, err := st.LatestAuditByAction("admin_login")
	if err != nil {
		t.Fatal(err)
	}
	if lastLogin.Action != "admin_login" || lastLogin.Details != "session established" {
		t.Fatalf("unexpected latest login audit: %#v", lastLogin)
	}
	if err := st.Audit("admin_login", "session established again"); err != nil {
		t.Fatal(err)
	}
	newerLogin, err := st.LatestAuditByAction("admin_login")
	if err != nil {
		t.Fatal(err)
	}
	if newerLogin.ID <= lastLogin.ID || newerLogin.Details != "session established again" {
		t.Fatalf("expected newest login audit, got %#v after %#v", newerLogin, lastLogin)
	}

	if _, err := st.LatestAuditByAction("admin_logout"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected missing latest logout audit lookup to return sql.ErrNoRows, got %v", err)
	}
	if err := st.Audit("admin_logout", "session revoked"); err != nil {
		t.Fatal(err)
	}
	lastLogout, err := st.LatestAuditByAction("admin_logout")
	if err != nil {
		t.Fatal(err)
	}
	if lastLogout.Action != "admin_logout" || lastLogout.Details != "session revoked" {
		t.Fatalf("unexpected latest logout audit: %#v", lastLogout)
	}
	if err := st.Audit("admin_logout", "session revoked again"); err != nil {
		t.Fatal(err)
	}
	newerLogout, err := st.LatestAuditByAction("admin_logout")
	if err != nil {
		t.Fatal(err)
	}
	if newerLogout.ID <= lastLogout.ID || newerLogout.Details != "session revoked again" {
		t.Fatalf("expected newest logout audit, got %#v after %#v", newerLogout, lastLogout)
	}
}

func TestBumpAdminSessionEpochIsAtomic(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	const bumps = 8
	errs := make(chan error, bumps)
	var wg sync.WaitGroup
	for range bumps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := st.BumpAdminSessionEpoch()
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	settings, err := st.GetAdminSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.AdminSessionEpoch != bumps {
		t.Fatalf("expected %d atomic bumps, got %d", bumps, settings.AdminSessionEpoch)
	}
}
