package sqliteuri

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestBuildEscapesURIDelimiters(t *testing.T) {
	tests := []struct {
		path   string
		params []string
		want   string
	}{
		{path: "/tmp/plain.sqlite", want: "file:/tmp/plain.sqlite"},
		{path: "/tmp/plain.sqlite", params: []string{"mode=ro"}, want: "file:/tmp/plain.sqlite?mode=ro"},
		{path: "/tmp/with space.sqlite", params: []string{"mode=ro", "_pragma=busy_timeout(5000)"}, want: "file:/tmp/with space.sqlite?mode=ro&_pragma=busy_timeout(5000)"},
		{path: "/tmp/what?.sqlite", want: "file:/tmp/what%3F.sqlite"},
		{path: "/tmp/#1 backup.sqlite", want: "file:/tmp/%231 backup.sqlite"},
		{path: "/tmp/100%.sqlite", want: "file:/tmp/100%25.sqlite"},
	}
	for _, tc := range tests {
		if got := Build(tc.path, tc.params...); got != tc.want {
			t.Errorf("Build(%q, %v) = %q, want %q", tc.path, tc.params, got, tc.want)
		}
	}
}

// A path containing '?' or '#' used to be truncated by the driver, so the
// database was silently created at a different location.
func TestBuildOpensPathsWithDelimiters(t *testing.T) {
	for _, name := range []string{"catalog #2.sqlite", "why?.sqlite", "50%.sqlite", "plain space.sqlite"} {
		path := filepath.Join(t.TempDir(), name)
		db, err := sql.Open("sqlite", Build(path, "_pragma=busy_timeout(5000)"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`create table t (id integer primary key)`); err != nil {
			_ = db.Close()
			t.Fatalf("create table in %q: %v", name, err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		readOnly, err := sql.Open("sqlite", Build(path, "mode=ro"))
		if err != nil {
			t.Fatal(err)
		}
		var count int
		if err := readOnly.QueryRow(`select count(*) from sqlite_master where name = 't'`).Scan(&count); err != nil {
			_ = readOnly.Close()
			t.Fatalf("query %q read-only: %v", name, err)
		}
		_ = readOnly.Close()
		if count != 1 {
			t.Fatalf("expected table in %q to be found through the escaped URI, got %d", name, count)
		}
	}
}
