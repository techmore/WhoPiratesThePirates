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
}
