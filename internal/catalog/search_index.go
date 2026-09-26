package catalog

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BuildSearchIndex creates an application-owned FTS5 index for a read-only
// catalog. The source database is never modified; only searchable text and
// the source rowid are copied into the derived index.
func BuildSearchIndex(sourcePath, indexPath string) error {
	if strings.TrimSpace(sourcePath) == "" || strings.TrimSpace(indexPath) == "" {
		return fmt.Errorf("source and index paths are required")
	}
	source, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)", sourcePath))
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
	index, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", temporaryPath))
	if err != nil {
		return err
	}
	removeTemporary := true
	defer func() {
		_ = index.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := index.Exec(`pragma journal_mode = off; pragma synchronous = off; pragma temp_store = memory; create virtual table torrent_search using fts5(content);`); err != nil {
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
