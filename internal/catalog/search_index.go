package catalog

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const searchIndexFormatVersion = "2"

type sourceFingerprint struct {
	path     string
	size     int64
	modified int64
}

func fingerprintSource(path string) (sourceFingerprint, error) {
	info, err := os.Stat(path)
	if err != nil {
		return sourceFingerprint{}, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return sourceFingerprint{}, err
	}
	return sourceFingerprint{path: filepath.Clean(absolute), size: info.Size(), modified: info.ModTime().UnixNano()}, nil
}

// SearchIndexMatches reports whether the persisted FTS index was built from
// the current version of the read-only source catalog.
func SearchIndexMatches(sourcePath, indexPath string) (bool, error) {
	source, err := fingerprintSource(sourcePath)
	if err != nil {
		return false, err
	}
	indexInfo, err := os.Stat(indexPath)
	if err != nil || !indexInfo.Mode().IsRegular() {
		return false, nil
	}
	index, err := openReadOnlyDatabase(indexPath, "")
	if err != nil {
		return false, nil
	}

	var format, indexedPath string
	var size, modified int64
	err = index.QueryRow(`select format_version, source_path, source_size, source_modified_ns from search_index_metadata where id = 1`).Scan(&format, &indexedPath, &size, &modified)
	if err == nil {
		_ = index.Close()
		return format == searchIndexFormatVersion && indexedPath == source.path && size == source.size && modified == source.modified, nil
	}

	var metadataTables int
	if err := index.QueryRow(`select count(*) from sqlite_master where type = 'table' and name = 'search_index_metadata'`).Scan(&metadataTables); err != nil || metadataTables != 0 {
		_ = index.Close()
		return false, nil
	}
	var searchTables int
	if err := index.QueryRow(`select count(*) from sqlite_master where type = 'table' and name = 'torrent_search'`).Scan(&searchTables); err != nil || searchTables != 1 {
		_ = index.Close()
		return false, nil
	}
	var rowID int64
	if err := index.QueryRow(`select rowid from torrent_search limit 1`).Scan(&rowID); err != nil {
		_ = index.Close()
		return false, nil
	}
	_ = index.Close()

	// Older releases keyed their durable index by source path. Keep those caches
	// when they postdate the source file, then stamp them so later checks can
	// detect in-place catalog replacement without rebuilding a multi-gigabyte
	// index during startup.
	if indexInfo.ModTime().UnixNano() < source.modified {
		return false, nil
	}
	adoptLegacySearchIndex(indexPath, source)
	return true, nil
}

func adoptLegacySearchIndex(indexPath string, source sourceFingerprint) {
	index, err := sql.Open("sqlite", sqliteFileURI(indexPath, nil))
	if err != nil {
		return
	}
	defer index.Close()
	index.SetMaxOpenConns(1)
	index.SetMaxIdleConns(1)
	if err := index.Ping(); err != nil {
		return
	}
	if _, err := index.Exec(`create table if not exists search_index_metadata (id integer primary key check (id = 1), format_version text not null, source_path text not null, source_size integer not null, source_modified_ns integer not null)`); err != nil {
		return
	}
	_, _ = index.Exec(`insert or replace into search_index_metadata (id, format_version, source_path, source_size, source_modified_ns) values (1, ?, ?, ?, ?)`, searchIndexFormatVersion, source.path, source.size, source.modified)
}

// BuildSearchIndex creates an application-owned FTS5 index for a read-only
// catalog. The source database is never modified; only searchable text and
// the source rowid are copied into the derived index.
func BuildSearchIndex(sourcePath, indexPath string) error {
	if strings.TrimSpace(sourcePath) == "" || strings.TrimSpace(indexPath) == "" {
		return fmt.Errorf("source and index paths are required")
	}
	fingerprint, err := fingerprintSource(sourcePath)
	if err != nil {
		return fmt.Errorf("inspect source catalog: %w", err)
	}
	source, err := openReadOnlyDatabase(sourcePath, "")
	if err != nil {
		return err
	}
	defer source.Close()
	if err := source.Ping(); err != nil {
		return fmt.Errorf("open source catalog: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(indexPath), 0o700); err != nil {
		return fmt.Errorf("create search index directory: %w", err)
	}
	temporaryPath := indexPath + ".building"
	if err := os.Remove(temporaryPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale search index: %w", err)
	}
	index, err := sql.Open("sqlite", sqliteFileURI(temporaryPath, nil))
	if err != nil {
		return err
	}
	index.SetMaxOpenConns(1)
	index.SetMaxIdleConns(1)
	removeTemporary := true
	defer func() {
		_ = index.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := index.Exec(`pragma journal_mode = off; pragma synchronous = off; pragma temp_store = memory; create virtual table torrent_search using fts5(content); create table search_index_metadata (id integer primary key check (id = 1), format_version text not null, source_path text not null, source_size integer not null, source_modified_ns integer not null);`); err != nil {
		return fmt.Errorf("create FTS5 search index: %w", err)
	}

	rows, err := source.Query(`select id, coalesce(name, ''), coalesce(description, ''), coalesce(infoHash, '') from torrents`)
	if err != nil {
		return fmt.Errorf("read source catalog for search index: %w", err)
	}
	defer rows.Close()

	var tx *sql.Tx
	var insert *sql.Stmt
	batch := 0
	commitBatch := func() error {
		if tx == nil {
			return nil
		}
		if err := insert.Close(); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		tx = nil
		insert = nil
		batch = 0
		return nil
	}
	for rows.Next() {
		if tx == nil {
			tx, err = index.Begin()
			if err != nil {
				return fmt.Errorf("start search index batch: %w", err)
			}
			insert, err = tx.Prepare(`insert into torrent_search(rowid, content) values(?, ?)`)
			if err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("prepare search index batch: %w", err)
			}
		}
		var id int64
		var name, description, infoHash string
		if err := rows.Scan(&id, &name, &description, &infoHash); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("read source catalog row: %w", err)
		}
		content := strings.TrimSpace(strings.Join([]string{name, description, infoHash}, "\n"))
		if _, err := insert.Exec(id, content); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("write search index row: %w", err)
		}
		batch++
		if batch >= 10000 {
			if err := commitBatch(); err != nil {
				return fmt.Errorf("commit search index batch: %w", err)
			}
		}
	}
	if err := rows.Err(); err != nil {
		if tx != nil {
			_ = tx.Rollback()
		}
		return fmt.Errorf("iterate source catalog: %w", err)
	}
	if err := commitBatch(); err != nil {
		return fmt.Errorf("commit final search index batch: %w", err)
	}
	if _, err := index.Exec(`insert into search_index_metadata (id, format_version, source_path, source_size, source_modified_ns) values (1, ?, ?, ?, ?)`, searchIndexFormatVersion, fingerprint.path, fingerprint.size, fingerprint.modified); err != nil {
		return fmt.Errorf("write search index metadata: %w", err)
	}
	if err := index.Close(); err != nil {
		return fmt.Errorf("close search index: %w", err)
	}
	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		return fmt.Errorf("secure search index: %w", err)
	}
	if err := os.Rename(temporaryPath, indexPath); err != nil {
		return fmt.Errorf("install search index: %w", err)
	}
	removeTemporary = false
	return nil
}
