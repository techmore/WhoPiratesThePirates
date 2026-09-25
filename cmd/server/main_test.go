package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestIsLoopbackAddress(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "localhost", value: "localhost", want: true},
		{name: "ipv4", value: "127.0.0.1", want: true},
		{name: "ipv6", value: "[::1]", want: true},
		{name: "wildcard", value: "0.0.0.0", want: false},
		{name: "public", value: "192.0.2.10", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLoopbackAddress(tc.value); got != tc.want {
				t.Fatalf("isLoopbackAddress(%q) = %t, want %t", tc.value, got, tc.want)
			}
		})
	}
}

func TestEnvEnabled(t *testing.T) {
	for _, value := range []string{"1", "true", "TRUE"} {
		if !envEnabled(value) {
			t.Fatalf("envEnabled(%q) = false, want true", value)
		}
	}
	for _, value := range []string{"", "0", "false"} {
		if envEnabled(value) {
			t.Fatalf("envEnabled(%q) = true, want false", value)
		}
	}
}

func TestRunSetupCreatesLocalConfiguration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHOP2P_HOME", home)
	t.Setenv("APP_DB_PATH", "")
	t.Setenv("APP_STATE_PATH", "")
	t.Setenv("APP_BIND_ADDR", "")
	t.Setenv("PORT", "")
	t.Setenv("ADMIN_PASSWORD", "")
	t.Setenv("ADMIN_SESSION_SECRET", "")

	if err := runSetup(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"catalog.sqlite", "app_state.sqlite", "service.env"} {
		if _, err := os.Stat(filepath.Join(home, name)); err != nil {
			t.Fatalf("expected %s to exist: %v", name, err)
		}
	}
	// A second setup must preserve the operator's existing files.
	if err := os.WriteFile(filepath.Join(home, "catalog.sqlite"), []byte("operator-owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runSetup(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(home, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "operator-owned" {
		t.Fatalf("setup overwrote an existing catalog: %q", contents)
	}
}

func TestRunLoadCatalogValidatesAndInstallsBackup(t *testing.T) {
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "source.sqlite")
	if err := createEmptyCatalog(source); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into torrents(id, category, status, name, numFiles, size, seeders, leechers, username, added, description, infoHash) values (1, 0, 'seeded', 'Ubuntu', 0, 1, 1, 0, 'tester', 1, 'fixture', '0123456789abcdef0123456789abcdef01234567')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	t.Setenv("WHOP2P_HOME", home)
	if err := runLoadCatalog(source); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(home, "catalog.sqlite")
	if _, err := os.Stat(installed); err != nil {
		t.Fatal(err)
	}

	invalid := filepath.Join(sourceDir, "invalid.sqlite")
	if err := os.WriteFile(invalid, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runLoadCatalog(invalid); err == nil {
		t.Fatal("expected invalid catalog to be rejected")
	}
}

func TestLoadServiceEnvDoesNotOverrideShellEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHOP2P_HOME", home)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "service.env"), []byte("PORT=9999\nAPP_BIND_ADDR=127.0.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PORT", "8081")
	if err := loadServiceEnv(); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("PORT") != "8081" {
		t.Fatalf("shell PORT was overridden: %q", os.Getenv("PORT"))
	}
	if os.Getenv("APP_BIND_ADDR") != "127.0.0.1" {
		t.Fatalf("expected service bind address to load, got %q", os.Getenv("APP_BIND_ADDR"))
	}
}
