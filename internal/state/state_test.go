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

	if err := st.CreateCatalogSource("Primary recovery", "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", "/backups/catalog.sqlite", true); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateCatalogSource("Hidden recovery", "", "/backups/hidden.sqlite", false); err != nil {
		t.Fatal(err)
	}
	catalogSources, err := st.CatalogSources()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalogSources) != 2 || catalogSources[0].Name != "Hidden recovery" || catalogSources[1].Magnet == "" {
		t.Fatalf("unexpected catalog sources: %#v", catalogSources)
	}
	enabledCatalogSources, err := st.EnabledCatalogSources()
	if err != nil {
		t.Fatal(err)
	}
	if len(enabledCatalogSources) != 1 || enabledCatalogSources[0].Name != "Primary recovery" {
		t.Fatalf("unexpected enabled catalog sources: %#v", enabledCatalogSources)
	}
	if err := st.UpdateCatalogSource(catalogSources[0].ID, "Hidden updated", "", "/backups/hidden-new.sqlite"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCatalogSourceEnabled(catalogSources[0].ID, true); err != nil {
		t.Fatal(err)
	}
	updatedCatalogSource, err := st.CatalogSource(catalogSources[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedCatalogSource.Name != "Hidden updated" || !updatedCatalogSource.Enabled || updatedCatalogSource.CatalogPath != "/backups/hidden-new.sqlite" {
		t.Fatalf("unexpected updated catalog source: %#v", updatedCatalogSource)
	}
	if err := st.DeleteCatalogSource(catalogSources[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CatalogSource(catalogSources[0].ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected deleted catalog source lookup to fail, got %v", err)
	}
	if err := st.SetActiveCatalog("Primary recovery", "/backups/catalog.sqlite"); err != nil {
		t.Fatal(err)
	}
	activeCatalog, err := st.ActiveCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if activeCatalog.Name != "Primary recovery" || activeCatalog.Path != "/backups/catalog.sqlite" {
		t.Fatalf("unexpected active catalog: %#v", activeCatalog)
	}
	if err := st.ClearActiveCatalog(); err != nil {
		t.Fatal(err)
	}
	activeCatalog, err = st.ActiveCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if activeCatalog.Name != "" || activeCatalog.Path != "" {
		t.Fatalf("expected active catalog to be cleared, got %#v", activeCatalog)
	}

	if err := st.CreateImportRun(sources[0].ID, "queued", "created for test", "abc123", "approved/ref"); err != nil {
		t.Fatal(err)
	}
	runs, err := st.ImportRuns(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != "queued" || runs[0].ApprovedRef != "approved/ref" || runs[0].FinishedAt != 0 {
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
	if len(runs) != 2 || runs[0].ApprovedRef != "approved/ref" || runs[0].FinishedAt != 0 {
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

// The admin console renders "1-10 of N" ranges from the count queries, so a
// count that filters differently from the page it describes would quietly lie
// to the operator. Assert the two stay in step for every searchable table.
func TestRecordCountsMatchFilteredPages(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sources := []struct {
		name, kind, location string
		enabled              bool
	}{
		{"Main index mirror", "https", "https://example.com/feed.json", true},
		{"Local dataset drop", "file", "/srv/authorized/dataset.json", false},
		{"Backup mirror", "https", "https://example.org/backup.json", true},
	}
	for _, source := range sources {
		if err := st.CreateImportSource(source.name, source.kind, source.location, source.enabled); err != nil {
			t.Fatal(err)
		}
	}

	runs := []struct {
		status, message, ref string
	}{
		{"succeeded", "Imported 4,201 records", "owner@example.com"},
		{"failed", "checksum mismatch", "owner@example.com"},
		{"queued", "awaiting operator approval", "ops@example.com"},
	}
	for _, run := range runs {
		if err := st.CreateImportRun(1, run.status, run.message, "", run.ref); err != nil {
			t.Fatal(err)
		}
	}

	manifests := []struct {
		name, approvedBy, baseDir string
	}{
		{"Authorized dataset 2024-06", "owner@example.com", "."},
		{"Archive mirror dataset", "ops@example.org", "/srv/authorized"},
	}
	for _, manifest := range manifests {
		if err := st.CreateImportManifest(manifest.name, manifest.approvedBy, manifest.baseDir, "sha256:aa", "sha256:aa", 2048); err != nil {
			t.Fatal(err)
		}
	}

	audits := []struct{ action, details string }{
		{"import_source_create", "name=Main index mirror"},
		{"import_source_toggle", "id=1 enabled=false"},
		{"import_run_finish", "id=1 status=succeeded"},
	}
	for _, entry := range audits {
		if err := st.Audit(entry.action, entry.details); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		label   string
		queries []string
		count   func(string) (int64, error)
		page    func(string, int, int) (int, error)
	}{
		{
			label:   "import sources",
			queries: []string{"", "example.com", "Backup", "file", "nothing-matches-this"},
			count:   st.ImportSourcesCount,
			page: func(q string, limit, offset int) (int, error) {
				items, err := st.ImportSourcesSearchOffset(q, limit, offset)
				return len(items), err
			},
		},
		{
			label:   "import runs",
			queries: []string{"", "succeeded", "checksum", "example.com", "nothing-matches-this"},
			count:   st.ImportRunsCount,
			page: func(q string, limit, offset int) (int, error) {
				items, err := st.ImportRunsSearchOffset(q, limit, offset)
				return len(items), err
			},
		},
		{
			label:   "import manifests",
			queries: []string{"", "2024-06", "ops@example.org", "/srv", "nothing-matches-this"},
			count:   st.ImportManifestsCount,
			page: func(q string, limit, offset int) (int, error) {
				items, err := st.ImportManifestsSearchOffset(q, limit, offset)
				return len(items), err
			},
		},
		{
			label:   "audit entries",
			queries: []string{"", "import_run_finish", "checksum", "id=1", "nothing-matches-this"},
			count:   st.AuditEntriesCount,
			page: func(q string, limit, offset int) (int, error) {
				items, err := st.AuditEntriesSearchOffset(q, limit, offset)
				return len(items), err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			for _, q := range tc.queries {
				total, err := tc.count(q)
				if err != nil {
					t.Fatalf("count(%q): %v", q, err)
				}
				// Walk the whole result set a page at a time; the rows handed to
				// the operator must add up to exactly what the count reported.
				seen := 0
				for offset := 0; ; offset += 2 {
					page, err := tc.page(q, 2, offset)
					if err != nil {
						t.Fatalf("page(%q, offset=%d): %v", q, offset, err)
					}
					seen += page
					if page < 2 {
						break
					}
				}
				if seen != int(total) {
					t.Fatalf("query %q: paged rows %d disagree with count %d", q, seen, total)
				}
			}
		})
	}
}
