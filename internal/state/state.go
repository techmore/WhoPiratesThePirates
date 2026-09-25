package state

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type AuditEntry struct {
	ID        int64  `json:"id"`
	Action    string `json:"action"`
	CreatedAt int64  `json:"createdAt"`
	Details   string `json:"details"`
}

type ImportSource struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Location  string `json:"location"`
	Enabled   bool   `json:"enabled"`
	CreatedAt int64  `json:"createdAt"`
}

type ImportRun struct {
	ID          int64  `json:"id"`
	SourceID    int64  `json:"sourceId"`
	Status      string `json:"status"`
	StartedAt   int64  `json:"startedAt"`
	FinishedAt  int64  `json:"finishedAt,omitempty"`
	Message     string `json:"message"`
	Checksum    string `json:"checksum"`
	ApprovedRef string `json:"approvedRef"`
}

type ImportManifest struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	ApprovedBy      string `json:"approvedBy"`
	BaseDir         string `json:"baseDir"`
	Checksum        string `json:"checksum"`
	PreviewChecksum string `json:"previewChecksum"`
	TotalBytes      int64  `json:"totalBytes"`
	CreatedAt       int64  `json:"createdAt"`
}

type ImportReference struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
	InfoHash  string `json:"infoHash,omitempty"`
	Name      string `json:"name,omitempty"`
	Trackers  string `json:"trackers,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

type AdminSettings struct {
	TorEnabled        bool   `json:"torEnabled"`
	TorMode           string `json:"torMode"`
	TorAutostart      bool   `json:"torAutostart"`
	OnionAddress      string `json:"onionAddress"`
	TorStatus         string `json:"torStatus"`
	TorControlAddr    string `json:"torControlAddr"`
	AdminSessionEpoch int64  `json:"adminSessionEpoch"`
}

func Open(path string) (*Store, error) {
	// Foreign-key enforcement is per SQLite connection, so configure it in the
	// data source name rather than relying on a one-time PRAGMA call.
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path))
	if err != nil {
		return nil, err
	}
	if err := ensureSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Healthy() error {
	return s.db.Ping()
}

func ensureSchema(db *sql.DB) error {
	stmts := []string{
		`create table if not exists settings (key text primary key, value text not null)`,
		`create table if not exists admin_audit (id integer primary key autoincrement, action text not null, created_at integer not null, details text not null)`,
		`create table if not exists import_sources (id integer primary key autoincrement, name text not null, kind text not null, location text not null, enabled integer not null default 1, created_at integer not null)`,
		`create table if not exists import_runs (id integer primary key autoincrement, source_id integer not null, status text not null, started_at integer not null, finished_at integer not null default 0, message text not null default '', checksum text not null default '', approved_ref text not null default '', foreign key(source_id) references import_sources(id))`,
		`create table if not exists import_manifests (id integer primary key autoincrement, name text not null, approved_by text not null, base_dir text not null, checksum text not null, preview_checksum text not null, total_bytes integer not null, created_at integer not null)`,
		`create table if not exists import_references (id integer primary key autoincrement, kind text not null, reference text not null, info_hash text not null default '', name text not null default '', trackers text not null default '', created_at integer not null)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetAdminSettings() (AdminSettings, error) {
	out := AdminSettings{TorMode: "off", TorStatus: "disabled"}
	rows, err := s.db.Query(`select key, value from settings`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return out, err
		}
		switch k {
		case "tor_enabled":
			out.TorEnabled = v == "true"
		case "tor_mode":
			out.TorMode = v
		case "tor_autostart":
			out.TorAutostart = v == "true"
		case "onion_address":
			out.OnionAddress = v
		case "tor_status":
			out.TorStatus = v
		case "tor_control_addr":
			out.TorControlAddr = v
		case "admin_session_epoch":
			out.AdminSessionEpoch, _ = strconv.ParseInt(v, 10, 64)
		}
	}
	return out, rows.Err()
}

func (s *Store) PutAdminSettings(in AdminSettings) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	values := map[string]string{
		"tor_enabled":         strconv.FormatBool(in.TorEnabled),
		"tor_mode":            in.TorMode,
		"tor_autostart":       strconv.FormatBool(in.TorAutostart),
		"onion_address":       in.OnionAddress,
		"tor_status":          in.TorStatus,
		"tor_control_addr":    in.TorControlAddr,
		"admin_session_epoch": strconv.FormatInt(in.AdminSessionEpoch, 10),
	}
	for k, v := range values {
		if _, err := tx.Exec(`insert into settings(key, value) values(?, ?) on conflict(key) do update set value=excluded.value`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) BumpAdminSessionEpoch() (int64, error) {
	var epoch int64
	err := s.db.QueryRow(`
		insert into settings(key, value) values('admin_session_epoch', '1')
		on conflict(key) do update set value = cast(settings.value as integer) + 1
		returning value`).Scan(&epoch)
	return epoch, err
}

func (s *Store) Audit(action, details string) error {
	_, err := s.db.Exec(`insert into admin_audit(action, created_at, details) values(?, ?, ?)`, action, time.Now().Unix(), details)
	return err
}

func (s *Store) AuditEntries(limit int) ([]AuditEntry, error) {
	return s.AuditEntriesOffset(limit, 0)
}

func (s *Store) AuditEntriesOffset(limit, offset int) ([]AuditEntry, error) {
	return s.AuditEntriesSearchOffset("", limit, offset)
}

func (s *Store) AuditEntriesSearchOffset(q string, limit, offset int) ([]AuditEntry, error) {
	sqlText := `select id, action, created_at, details from admin_audit`
	var args []any
	if strings.TrimSpace(q) != "" {
		sqlText += ` where action like '%' || ? || '%' or details like '%' || ? || '%'`
		args = append(args, q, q)
	}
	sqlText += ` order by id desc limit ? offset ?`
	args = append(args, limit, offset)
	rows, err := s.db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AuditEntry, 0, limit)
	for rows.Next() {
		var item AuditEntry
		if err := rows.Scan(&item.ID, &item.Action, &item.CreatedAt, &item.Details); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) AuditEntry(id int64) (AuditEntry, error) {
	var item AuditEntry
	row := s.db.QueryRow(`select id, action, created_at, details from admin_audit where id = ?`, id)
	if err := row.Scan(&item.ID, &item.Action, &item.CreatedAt, &item.Details); err != nil {
		return AuditEntry{}, err
	}
	return item, nil
}

func (s *Store) AuditTotals() (count int64, err error) {
	row := s.db.QueryRow(`select count(*) from admin_audit`)
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) LatestAuditByAction(action string) (AuditEntry, error) {
	var item AuditEntry
	row := s.db.QueryRow(`select id, action, created_at, details from admin_audit where action = ? order by id desc limit 1`, action)
	if err := row.Scan(&item.ID, &item.Action, &item.CreatedAt, &item.Details); err != nil {
		return AuditEntry{}, err
	}
	return item, nil
}

func (s *Store) DeleteAuditEntry(id int64) error {
	_, err := s.db.Exec(`delete from admin_audit where id = ?`, id)
	return err
}

func (s *Store) ImportSources() ([]ImportSource, error) {
	return s.ImportSourcesOffset(1000, 0)
}

func (s *Store) ImportSourcesOffset(limit, offset int) ([]ImportSource, error) {
	return s.ImportSourcesSearchOffset("", limit, offset)
}

func (s *Store) ImportSourcesSearchOffset(q string, limit, offset int) ([]ImportSource, error) {
	sqlText := `select id, name, kind, location, enabled, created_at from import_sources`
	var args []any
	if strings.TrimSpace(q) != "" {
		sqlText += ` where name like '%' || ? || '%' or kind like '%' || ? || '%' or location like '%' || ? || '%'`
		args = append(args, q, q, q)
	}
	sqlText += ` order by id desc limit ? offset ?`
	args = append(args, limit, offset)
	rows, err := s.db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ImportSource, 0, limit)
	for rows.Next() {
		var item ImportSource
		var enabled int
		if err := rows.Scan(&item.ID, &item.Name, &item.Kind, &item.Location, &enabled, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.Enabled = enabled != 0
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ImportSource(id int64) (ImportSource, error) {
	var item ImportSource
	row := s.db.QueryRow(`select id, name, kind, location, enabled, created_at from import_sources where id = ?`, id)
	var enabled int
	if err := row.Scan(&item.ID, &item.Name, &item.Kind, &item.Location, &enabled, &item.CreatedAt); err != nil {
		return ImportSource{}, err
	}
	item.Enabled = enabled != 0
	return item, nil
}

func (s *Store) ImportSourceTotals() (count int64, err error) {
	row := s.db.QueryRow(`select count(*) from import_sources`)
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) CreateImportSource(name, kind, location string, enabled bool) error {
	_, err := s.db.Exec(
		`insert into import_sources(name, kind, location, enabled, created_at) values(?, ?, ?, ?, ?)`,
		name, kind, location, boolToInt(enabled), time.Now().Unix(),
	)
	return err
}

func (s *Store) SetImportSourceEnabled(id int64, enabled bool) error {
	_, err := s.db.Exec(`update import_sources set enabled = ? where id = ?`, boolToInt(enabled), id)
	return err
}

func (s *Store) UpdateImportSource(id int64, name, kind, location string) error {
	_, err := s.db.Exec(
		`update import_sources set name = ?, kind = ?, location = ? where id = ?`,
		name, kind, location, id,
	)
	return err
}

func (s *Store) DeleteImportSource(id int64) error {
	_, err := s.db.Exec(`delete from import_sources where id = ?`, id)
	return err
}

func (s *Store) CreateImportRun(sourceID int64, status, message, checksum, approvedRef string) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(
		`insert into import_runs(source_id, status, started_at, finished_at, message, checksum, approved_ref) values(?, ?, ?, ?, ?, ?, ?)`,
		sourceID, status, now, 0, message, checksum, approvedRef,
	)
	return err
}

func (s *Store) CreateImportManifest(name, approvedBy, baseDir, checksum, previewChecksum string, totalBytes int64) error {
	_, err := s.db.Exec(
		`insert into import_manifests(name, approved_by, base_dir, checksum, preview_checksum, total_bytes, created_at) values(?, ?, ?, ?, ?, ?, ?)`,
		name, approvedBy, baseDir, checksum, previewChecksum, totalBytes, time.Now().Unix(),
	)
	return err
}

// RecordValidatedManifest records the manifest and its queued import run as a
// single state transition, so an operator never sees one without the other.
func (s *Store) RecordValidatedManifest(sourceID int64, status, message, approvedRef, name, approvedBy, baseDir, checksum, previewChecksum string, totalBytes int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	if _, err := tx.Exec(
		`insert into import_runs(source_id, status, started_at, finished_at, message, checksum, approved_ref) values(?, ?, ?, ?, ?, ?, ?)`,
		sourceID, status, now, 0, message, previewChecksum, approvedRef,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`insert into import_manifests(name, approved_by, base_dir, checksum, preview_checksum, total_bytes, created_at) values(?, ?, ?, ?, ?, ?, ?)`,
		name, approvedBy, baseDir, checksum, previewChecksum, totalBytes, now,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ImportManifests(limit int) ([]ImportManifest, error) {
	return s.ImportManifestsOffset(limit, 0)
}

func (s *Store) ImportManifestsOffset(limit, offset int) ([]ImportManifest, error) {
	return s.ImportManifestsSearchOffset("", limit, offset)
}

func (s *Store) ImportManifestsSearchOffset(q string, limit, offset int) ([]ImportManifest, error) {
	sqlText := `select id, name, approved_by, base_dir, checksum, preview_checksum, total_bytes, created_at from import_manifests`
	var args []any
	if strings.TrimSpace(q) != "" {
		sqlText += ` where name like '%' || ? || '%' or approved_by like '%' || ? || '%' or base_dir like '%' || ? || '%'`
		args = append(args, q, q, q)
	}
	sqlText += ` order by id desc limit ? offset ?`
	args = append(args, limit, offset)
	rows, err := s.db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ImportManifest, 0, limit)
	for rows.Next() {
		var item ImportManifest
		if err := rows.Scan(&item.ID, &item.Name, &item.ApprovedBy, &item.BaseDir, &item.Checksum, &item.PreviewChecksum, &item.TotalBytes, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ImportManifest(id int64) (ImportManifest, error) {
	var item ImportManifest
	row := s.db.QueryRow(`select id, name, approved_by, base_dir, checksum, preview_checksum, total_bytes, created_at from import_manifests where id = ?`, id)
	if err := row.Scan(&item.ID, &item.Name, &item.ApprovedBy, &item.BaseDir, &item.Checksum, &item.PreviewChecksum, &item.TotalBytes, &item.CreatedAt); err != nil {
		return ImportManifest{}, err
	}
	return item, nil
}

func (s *Store) ImportManifestTotals() (count int64, totalBytes int64, err error) {
	row := s.db.QueryRow(`select count(*), coalesce(sum(total_bytes), 0) from import_manifests`)
	if err := row.Scan(&count, &totalBytes); err != nil {
		return 0, 0, err
	}
	return count, totalBytes, nil
}

func (s *Store) CreateImportReference(kind, reference, infoHash, name, trackers string) (ImportReference, error) {
	item := ImportReference{
		Kind:      kind,
		Reference: reference,
		InfoHash:  infoHash,
		Name:      name,
		Trackers:  trackers,
		CreatedAt: time.Now().Unix(),
	}
	result, err := s.db.Exec(`insert into import_references(kind, reference, info_hash, name, trackers, created_at) values(?, ?, ?, ?, ?, ?)`, item.Kind, item.Reference, item.InfoHash, item.Name, item.Trackers, item.CreatedAt)
	if err != nil {
		return ImportReference{}, err
	}
	item.ID, err = result.LastInsertId()
	return item, err
}

func (s *Store) ImportReferences(limit int) ([]ImportReference, error) {
	rows, err := s.db.Query(`select id, kind, reference, info_hash, name, trackers, created_at from import_references order by id desc limit ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ImportReference, 0, limit)
	for rows.Next() {
		var item ImportReference
		if err := rows.Scan(&item.ID, &item.Kind, &item.Reference, &item.InfoHash, &item.Name, &item.Trackers, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) DeleteImportReference(id int64) error {
	_, err := s.db.Exec(`delete from import_references where id = ?`, id)
	return err
}

func (s *Store) DeleteImportManifest(id int64) error {
	_, err := s.db.Exec(`delete from import_manifests where id = ?`, id)
	return err
}

func (s *Store) FinishImportRun(id int64, status, message string) error {
	_, err := s.db.Exec(
		`update import_runs set status = ?, finished_at = ?, message = ? where id = ?`,
		status, time.Now().Unix(), message, id,
	)
	return err
}

func (s *Store) ImportRuns(limit int) ([]ImportRun, error) {
	return s.ImportRunsOffset(limit, 0)
}

func (s *Store) ImportRunsOffset(limit, offset int) ([]ImportRun, error) {
	return s.ImportRunsSearchOffset("", limit, offset)
}

func (s *Store) ImportRunsSearchOffset(q string, limit, offset int) ([]ImportRun, error) {
	sqlText := `select id, source_id, status, started_at, finished_at, message, checksum, approved_ref from import_runs`
	var args []any
	if strings.TrimSpace(q) != "" {
		sqlText += ` where status like '%' || ? || '%' or message like '%' || ? || '%' or approved_ref like '%' || ? || '%'`
		args = append(args, q, q, q)
	}
	sqlText += ` order by id desc limit ? offset ?`
	args = append(args, limit, offset)
	rows, err := s.db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ImportRun, 0, limit)
	for rows.Next() {
		var item ImportRun
		if err := rows.Scan(&item.ID, &item.SourceID, &item.Status, &item.StartedAt, &item.FinishedAt, &item.Message, &item.Checksum, &item.ApprovedRef); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ImportRun(id int64) (ImportRun, error) {
	var item ImportRun
	row := s.db.QueryRow(`select id, source_id, status, started_at, finished_at, message, checksum, approved_ref from import_runs where id = ?`, id)
	if err := row.Scan(&item.ID, &item.SourceID, &item.Status, &item.StartedAt, &item.FinishedAt, &item.Message, &item.Checksum, &item.ApprovedRef); err != nil {
		return ImportRun{}, err
	}
	return item, nil
}

func (s *Store) ImportRunTotals() (count int64, err error) {
	row := s.db.QueryRow(`select count(*) from import_runs`)
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) DeleteImportRun(id int64) error {
	_, err := s.db.Exec(`delete from import_runs where id = ?`, id)
	return err
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
