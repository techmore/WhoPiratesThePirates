package catalog

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestCatalogQueries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	stmts := []string{
		`create table categories (id integer primary key, name text)`,
		`create table torrents (id integer primary key, category integer, status text, name text, numFiles integer, size real, seeders integer, leechers integer, username text, added integer, description text, imdb text, language text, textLanguage text, infoHash text)`,
		`create table files (id integer primary key, parentTorrentId integer, name text, size real)`,
		`create table yts_movies (id integer primary key)`,
		`create table yts_torrent_data (id integer primary key)`,
		`insert into categories(id, name) values (1, 'Movies')`,
		`insert into torrents(id, category, status, name, numFiles, size, seeders, leechers, username, added, description, imdb, language, textLanguage, infoHash) values (10, 1, 'ok', 'Test Torrent', 3, 1048576, 7, 2, 'alice', 1710000000, 'hello world', 'tt1234567', 'English', 'English', 'abcdef')`,
		`insert into files(id, parentTorrentId, name, size) values (1, 10, 'file1.mkv', 1024), (2, 10, 'file2.srt', 2048)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}

	cat, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	var ftsTables int
	if err := db.QueryRow(`select count(*) from sqlite_master where type = 'table' and name = 'torrent_fts'`).Scan(&ftsTables); err != nil {
		t.Fatal(err)
	}
	if ftsTables != 0 {
		t.Fatal("opening a catalog must not create derived indexes in the read-only database")
	}

	stats, err := cat.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Torrents != 1 || stats.Files != 2 || stats.Categories != 1 {
		t.Fatalf("unexpected stats: %#v", stats)
	}

	categories, err := cat.Categories()
	if err != nil {
		t.Fatal(err)
	}
	if len(categories) != 1 || categories[0].Name != "Movies" {
		t.Fatalf("unexpected categories: %#v", categories)
	}

	results, err := cat.Search("Test", "", "seeders", "desc", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Name != "Test Torrent" || results[0].CategoryName != "Movies" {
		t.Fatalf("unexpected search results: %#v", results)
	}

	results, err = cat.Search("hello", "", "seeders", "desc", 10, 0)
	if err != nil || len(results) != 1 {
		t.Fatalf("expected read-only description search to work, results=%#v err=%v", results, err)
	}
	results, err = cat.Search("100%_", "", "seeders", "desc", 10, 0)
	if err != nil {
		t.Fatalf("escaped wildcard search failed: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("wildcards must be treated literally, got %#v", results)
	}

	torrent, categoryName, err := cat.Torrent(10)
	if err != nil {
		t.Fatal(err)
	}
	if torrent.Name != "Test Torrent" || categoryName != "Movies" || torrent.InfoHash != "abcdef" {
		t.Fatalf("unexpected torrent: %#v %q", torrent, categoryName)
	}

	files, err := cat.TorrentFiles(10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].SizeHuman == "" {
		t.Fatalf("unexpected files: %#v", files)
	}

	if _, err := db.Exec(`insert into torrents(id, category, status, name, numFiles, size, seeders, leechers, username, added, description, infoHash) values (11, 1, 'ok', 'Newer Torrent', 1, 2048, 1, 0, 'bob', 1710000100, 'newer', 'fedcba')`); err != nil {
		t.Fatal(err)
	}
	results, total, err := cat.SearchPage("torrent", "", "newest", "asc", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(results) != 1 || results[0].ID != 11 {
		t.Fatalf("unexpected newest page: total=%d results=%#v", total, results)
	}
	results, total, err = cat.SearchPage("torrent", "1", "newest", "asc", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(results) != 1 || results[0].ID != 10 {
		t.Fatalf("unexpected second page: total=%d results=%#v", total, results)
	}
	results, total, err = cat.SearchPage("torrent", "", "hot", "desc", -1, -1)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(results) != 0 {
		t.Fatalf("expected invalid page bounds to return no rows safely, total=%d results=%#v", total, results)
	}
}
