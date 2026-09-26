package catalog

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

type Catalog struct {
	db            *sql.DB
	searchIndexed bool
}

type Category struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Torrent struct {
	ID           int64   `json:"id"`
	Category     int64   `json:"category"`
	CategoryName string  `json:"categoryName,omitempty"`
	Status       string  `json:"status"`
	Name         string  `json:"name"`
	NumFiles     int64   `json:"numFiles"`
	Size         float64 `json:"size"`
	Seeders      int64   `json:"seeders"`
	Leechers     int64   `json:"leechers"`
	Username     string  `json:"username"`
	Added        int64   `json:"added"`
	Description  *string `json:"description,omitempty"`
	IMDB         *string `json:"imdb,omitempty"`
	Language     *string `json:"language,omitempty"`
	TextLanguage *string `json:"textLanguage,omitempty"`
	InfoHash     string  `json:"infoHash"`
}

type TorrentFile struct {
	Name      string  `json:"name"`
	Size      float64 `json:"size"`
	SizeHuman string  `json:"sizeHuman"`
}

type Stats struct {
	Torrents       int64 `json:"torrents"`
	Files          int64 `json:"files"`
	Categories     int64 `json:"categories"`
	YTSMovies      int64 `json:"yts_movies"`
	YTSTorrentData int64 `json:"yts_torrent_data"`
}

func Open(path string) (*Catalog, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)", path))
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Catalog{db: db}, nil
}

func OpenWithSearchIndex(path, indexPath string) (*Catalog, error) {
	catalog, err := Open(path)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(indexPath) == "" {
		return catalog, nil
	}
	indexURI := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)", indexPath)
	if _, err := catalog.db.Exec(`attach database ? as search_index`, indexURI); err != nil {
		_ = catalog.Close()
		return nil, fmt.Errorf("attach search index: %w", err)
	}
	catalog.searchIndexed = true
	return catalog, nil
}

// Validate checks that path is an intact, non-empty catalog that exposes the
// tables required by the read-only query layer. It never modifies the source
// database.
func Validate(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", path)
	}

	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)", path))
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return err
	}

	var integrity string
	if err := db.QueryRow(`pragma integrity_check`).Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("sqlite integrity check returned %q", integrity)
	}
	for _, table := range []string{"torrents", "files", "categories", "yts_movies", "yts_torrent_data"} {
		var name string
		err := db.QueryRow(`select name from sqlite_master where type = 'table' and name = ?`, table).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("required table %q is missing", table)
		}
		if err != nil {
			return err
		}
	}
	var torrents int64
	if err := db.QueryRow(`select count(*) from torrents`).Scan(&torrents); err != nil {
		return err
	}
	if torrents == 0 {
		return errors.New("catalog contains no torrents")
	}
	return nil
}

func (c *Catalog) Close() error { return c.db.Close() }

func (c *Catalog) Healthy() error {
	return c.db.Ping()
}

func (c *Catalog) Stats() (Stats, error) {
	var s Stats
	if err := c.db.QueryRow(`select count(*) from torrents`).Scan(&s.Torrents); err != nil {
		return s, err
	}
	if err := c.db.QueryRow(`select count(*) from files`).Scan(&s.Files); err != nil {
		return s, err
	}
	if err := c.db.QueryRow(`select count(*) from categories`).Scan(&s.Categories); err != nil {
		return s, err
	}
	if err := c.db.QueryRow(`select count(*) from yts_movies`).Scan(&s.YTSMovies); err != nil {
		return s, err
	}
	if err := c.db.QueryRow(`select count(*) from yts_torrent_data`).Scan(&s.YTSTorrentData); err != nil {
		return s, err
	}
	return s, nil
}

func (c *Catalog) Categories() ([]Category, error) {
	rows, err := c.db.Query(`select id, coalesce(name, '') from categories order by id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Category
	for rows.Next() {
		var item Category
		if err := rows.Scan(&item.ID, &item.Name); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (c *Catalog) Search(q, category, sortField, sortDir string, limit, offset int) ([]Torrent, error) {
	indexed := c.searchIndexed && strings.TrimSpace(q) != ""
	orderBy, where, args := searchQueryFor(q, category, sortField, sortDir, indexed)
	limit, offset = normalizePage(limit, offset)
	return c.searchItems(orderBy, where, args, indexed, limit, offset)
}

// SearchPage returns one page of results and the total number of matching
// records. The catalog is read-only, so the count is intentionally computed
// with the same predicate as the page query rather than relying on a derived
// table that may not exist in the source database.
func (c *Catalog) SearchPage(q, category, sortField, sortDir string, limit, offset int) ([]Torrent, int64, error) {
	indexed := c.searchIndexed && strings.TrimSpace(q) != ""
	orderBy, where, args := searchQueryFor(q, category, sortField, sortDir, indexed)
	limit, offset = normalizePage(limit, offset)

	var total int64
	searchJoin := ""
	if indexed {
		searchJoin = ` join search_index.torrent_search on search_index.torrent_search.rowid = t.id`
	}
	if err := c.db.QueryRow(`select count(*) from torrents t`+searchJoin+` left join categories c on c.id = t.category where `+strings.Join(where, ` and `), args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	items, err := c.searchItems(orderBy, where, args, indexed, limit, offset)
	return items, total, err
}

func (c *Catalog) searchItems(orderBy string, where []string, args []any, useSearchIndex bool, limit, offset int) ([]Torrent, error) {
	searchJoin := ""
	if useSearchIndex {
		searchJoin = ` join search_index.torrent_search on search_index.torrent_search.rowid = t.id`
	}
	query := `
		select t.id, t.category, t.status, t.name, t.numFiles, t.size, t.seeders, t.leechers, t.username, t.added, t.description, t.imdb, t.language, t.textLanguage, t.infoHash, coalesce(c.name, '')
		from torrents t` + searchJoin + `
		left join categories c on c.id = t.category
		where ` + strings.Join(where, ` and `) + ` order by ` + orderBy + ` limit ? offset ?`
	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := c.db.Query(query, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Torrent, 0, limit)
	for rows.Next() {
		var item Torrent
		if err := rows.Scan(&item.ID, &item.Category, &item.Status, &item.Name, &item.NumFiles, &item.Size, &item.Seeders, &item.Leechers, &item.Username, &item.Added, &item.Description, &item.IMDB, &item.Language, &item.TextLanguage, &item.InfoHash, &item.CategoryName); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func normalizePage(limit, offset int) (int, int) {
	if limit < 0 {
		limit = 0
	}
	if limit > 1000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	if offset > 1000000 {
		offset = 1000000
	}
	return limit, offset
}

func searchQuery(q, category, sortField, sortDir string) (string, []string, []any) {
	return searchQueryFor(q, category, sortField, sortDir, false)
}

func searchQueryFor(q, category, sortField, sortDir string, useSearchIndex bool) (string, []string, []any) {
	orderBy := "seeders desc, added desc"
	dir := "asc"
	if strings.EqualFold(sortDir, "desc") {
		dir = "desc"
	}
	switch sortField {
	case "name":
		orderBy = "name " + dir + ", seeders desc, added desc"
	case "category":
		orderBy = "category " + dir + ", name asc"
	case "size":
		orderBy = "size " + dir + ", seeders desc"
	case "seeders":
		orderBy = "seeders " + dir + ", leechers asc, added desc"
	case "leechers":
		orderBy = "leechers " + dir + ", seeders desc, added desc"
	case "newest":
		orderBy = "added desc, seeders desc"
	case "added":
		orderBy = "added " + dir + ", seeders desc"
	case "", "hot":
		orderBy = "seeders desc, added desc"
	}

	where := []string{`(? = '' or t.category = ?)`}
	args := make([]any, 0, 6)
	args = append(args, category, category)
	if q = strings.TrimSpace(q); q != "" {
		if useSearchIndex {
			where = append(where, `search_index.torrent_search.content match ?`)
			args = append(args, ftsQuery(q))
		} else {
			// LIKE keeps the catalog connection strictly read-only when no derived
			// search index has been built yet.
			where = append(where, `(coalesce(t.name, '') like ? escape '\' or coalesce(t.description, '') like ? escape '\' or coalesce(t.infoHash, '') like ? escape '\')`)
			pattern := "%" + escapeLike(q) + "%"
			args = append(args, pattern, pattern, pattern)
		}
	}
	return orderBy, where, args
}

func ftsQuery(q string) string {
	parts := make([]string, 0)
	for _, part := range strings.Fields(q) {
		part = strings.ReplaceAll(part, `"`, `""`)
		if part != "" {
			parts = append(parts, `"`+part+`"`)
		}
	}
	if len(parts) == 0 {
		return `*`
	}
	return strings.Join(parts, " AND ")
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	return strings.ReplaceAll(s, "_", "\\_")
}

func (c *Catalog) Torrent(id int64) (Torrent, string, error) {
	var t Torrent
	var categoryName string
	err := c.db.QueryRow(`
		select t.id, t.category, t.status, t.name, t.numFiles, t.size, t.seeders, t.leechers, t.username, t.added, t.description, t.imdb, t.language, t.textLanguage, t.infoHash, coalesce(c.name, '')
		from torrents t
		left join categories c on c.id = t.category
		where t.id = ?`, id).Scan(&t.ID, &t.Category, &t.Status, &t.Name, &t.NumFiles, &t.Size, &t.Seeders, &t.Leechers, &t.Username, &t.Added, &t.Description, &t.IMDB, &t.Language, &t.TextLanguage, &t.InfoHash, &categoryName)
	if err != nil {
		return Torrent{}, "", err
	}
	t.CategoryName = categoryName
	return t, categoryName, nil
}

func (c *Catalog) TorrentFiles(id int64, limit int) ([]TorrentFile, error) {
	rows, err := c.db.Query(`select coalesce(name, ''), size from files where parentTorrentId = ? order by id limit ?`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := make([]TorrentFile, 0, 16)
	for rows.Next() {
		var f TorrentFile
		if err := rows.Scan(&f.Name, &f.Size); err != nil {
			return nil, err
		}
		f.SizeHuman = formatBytes(f.Size)
		files = append(files, f)
	}
	return files, rows.Err()
}

func formatBytes(n float64) string {
	switch {
	case n >= 1024*1024*1024:
		return fmt.Sprintf("%.2f GB", n/1024/1024/1024)
	case n >= 1024*1024:
		return fmt.Sprintf("%.2f MB", n/1024/1024)
	case n >= 1024:
		return fmt.Sprintf("%.2f KB", n/1024)
	default:
		return fmt.Sprintf("%.0f B", n)
	}
}
