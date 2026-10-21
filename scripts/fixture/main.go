// Command fixture writes a small synthetic catalog database for local
// development and the end-to-end test. It contains metadata only (no content
// is fetched or seeded) and does not require the sqlite3 CLI.
package main

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/fixture <catalog.sqlite>")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "fixture:", err)
		os.Exit(1)
	}
}

func run(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	statements := []string{
		`create table categories (id integer primary key, name text)`,
		`create table torrents (id integer primary key, category integer, status text, name text, numFiles integer, size real, seeders integer, leechers integer, username text, added integer, description text, imdb text, language text, textLanguage text, infoHash text)`,
		`create table files (id integer primary key, parentTorrentId integer, name text, size real)`,
		`create table yts_movies (id integer primary key)`,
		`create table yts_torrent_data (id integer primary key)`,
		`insert into categories(id, name) values (1, 'Linux'), (2, 'Documentation'), (3, 'Public Domain Audio')`,
		`insert into torrents values (1, 1, 'seeded', 'Ubuntu 24.04 LTS Desktop amd64', 1, 5970000000, 100, 10, 'ubuntu-torrent', 1710000000, 'Official Ubuntu desktop image', 'tt1234567', 'English', 'English', '0123456789abcdef0123456789abcdef01234567')`,
		`insert into torrents values (2, 1, 'seeded', 'Debian 12.5 netinst amd64', 1, 650000000, 42, 3, 'debian-cd', 1711000000, 'Debian network installer', '', 'English', '', 'ABCDEF0123456789ABCDEF0123456789ABCDEF01')`,
		`insert into torrents values (3, 2, 'seeded', 'Project Gutenberg Collection 2023', 400, 1200000000, 5, 1, 'gutenberg', 1690000000, '', '', '', '', 'badhash')`,
		`insert into torrents values (4, 3, 'seeded', 'LibriVox Audio Sampler', 12, 90000, 0, 0, 'librivox', 1600000000, 'tiny', '', '', '', 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567')`,
		`insert into files values (1, 1, 'ubuntu-24.04-desktop-amd64.iso', 5970000000)`,
		`insert into files values (2, 2, 'debian-12.5.0-amd64-netinst.iso', 650000000)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}
