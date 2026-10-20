package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
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

func TestLoadServiceEnvPreservesPathsWithSpaces(t *testing.T) {
	home := filepath.Join(t.TempDir(), "Application Support", "WhoPiratesThePirates")
	t.Setenv("WHOP2P_HOME", home)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	catalog := filepath.Join(home, "catalog.sqlite")
	if err := os.WriteFile(filepath.Join(home, "service.env"), []byte("APP_DB_PATH="+catalog+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_DB_PATH", "")
	if err := loadServiceEnv(); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("APP_DB_PATH") != catalog {
		t.Fatalf("expected path with spaces to load unchanged, got %q", os.Getenv("APP_DB_PATH"))
	}
}

func TestRunDispatchesCommandsAndExitCodes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHOP2P_HOME", home)

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "help", args: []string{"help"}, wantCode: exitOK, wantStdout: "Usage:"},
		{name: "help flag", args: []string{"--help"}, wantCode: exitOK, wantStdout: "APP_DB_PATH"},
		{name: "short help flag", args: []string{"-h"}, wantCode: exitOK, wantStdout: "load-catalog FILE"},
		{name: "version", args: []string{"version"}, wantCode: exitOK, wantStdout: version},
		{name: "version flag", args: []string{"--version"}, wantCode: exitOK, wantStdout: version},
		{name: "unknown command", args: []string{"setup2"}, wantCode: exitUsage, wantStderr: `unknown command "setup2"`},
		{name: "serve with arguments", args: []string{"serve", "extra"}, wantCode: exitUsage, wantStderr: "serve does not take arguments"},
		{name: "setup with arguments", args: []string{"setup", "extra"}, wantCode: exitUsage, wantStderr: "setup does not take arguments"},
		{name: "load-catalog without file", args: []string{"load-catalog"}, wantCode: exitUsage, wantStderr: "exactly one argument"},
		{name: "load-catalog with two files", args: []string{"load-catalog", "a", "b"}, wantCode: exitUsage, wantStderr: "exactly one argument"},
		{name: "load-catalog missing file", args: []string{"load-catalog", filepath.Join(home, "missing.sqlite")}, wantCode: exitFailure, wantStderr: "catalog validation failed"},
		{name: "open with arguments", args: []string{"open", "extra"}, wantCode: exitUsage, wantStderr: "open does not take arguments"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tc.args, &stdout, &stderr)
			if code != tc.wantCode {
				t.Fatalf("run(%v) exit %d, want %d (stdout=%q stderr=%q)", tc.args, code, tc.wantCode, stdout.String(), stderr.String())
			}
			if tc.wantStdout != "" && !strings.Contains(stdout.String(), tc.wantStdout) {
				t.Fatalf("stdout %q does not contain %q", stdout.String(), tc.wantStdout)
			}
			if tc.wantStderr != "" && !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Fatalf("stderr %q does not contain %q", stderr.String(), tc.wantStderr)
			}
			if code == exitUsage && !strings.Contains(stderr.String(), "Usage:") {
				t.Fatalf("usage error should print usage to stderr, got %q", stderr.String())
			}
		})
	}
}

func TestUsageDocumentsEveryEnvironmentVariable(t *testing.T) {
	var out bytes.Buffer
	printUsage(&out)
	for _, name := range []string{
		"APP_DB_PATH", "APP_STATE_PATH", "APP_BIND_ADDR", "PORT", "ADMIN_PASSWORD", "ADMIN_SESSION_SECRET",
		"APP_ALLOWED_HOSTS", "APP_ARIA2_PATH", "APP_ARCHIVE_EXTRACTOR", "APP_DOWNLOAD_DIR", "APP_TLS_CERT_FILE",
		"APP_TLS_KEY_FILE", "APP_COOKIE_SECURE", "APP_ALLOW_INSECURE_HTTP", "WHOP2P_HOME", "WHOP2P_URL",
	} {
		if !strings.Contains(out.String(), name) {
			t.Fatalf("usage text does not document %s", name)
		}
	}
}

func TestRunServeRejectsBadConfiguration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WHOP2P_HOME", home)
	t.Setenv("APP_DB_PATH", filepath.Join(home, "missing.sqlite"))
	t.Setenv("APP_STATE_PATH", filepath.Join(home, "state.sqlite"))

	t.Setenv("PORT", "notaport")
	if err := runServe(); err == nil || !strings.Contains(err.Error(), "invalid PORT") {
		t.Fatalf("expected invalid PORT to be rejected, got %v", err)
	}
	t.Setenv("PORT", "0")
	if err := runServe(); err == nil || !strings.Contains(err.Error(), "invalid PORT") {
		t.Fatalf("expected PORT 0 to be rejected, got %v", err)
	}

	t.Setenv("PORT", "18099")
	t.Setenv("APP_BIND_ADDR", "0.0.0.0")
	t.Setenv("APP_ALLOW_INSECURE_HTTP", "")
	if err := runServe(); err == nil || !strings.Contains(err.Error(), "refusing insecure HTTP") {
		t.Fatalf("expected non-loopback plaintext bind to be refused, got %v", err)
	}

	t.Setenv("APP_BIND_ADDR", "127.0.0.1")
	t.Setenv("APP_TLS_CERT_FILE", filepath.Join(home, "cert.pem"))
	t.Setenv("APP_TLS_KEY_FILE", "")
	if err := runServe(); err == nil || !strings.Contains(err.Error(), "configured together") {
		t.Fatalf("expected half-configured TLS to be refused, got %v", err)
	}
}
