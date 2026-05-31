package catalog

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

type Catalog struct {
	db *sql.DB
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
	if err := ensureFTSIndex(path); err != nil {
		return nil, err
	}
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
	case "added":
		orderBy = "added " + dir + ", seeders desc"
	case "", "hot":
		orderBy = "seeders desc, added desc"
	}

	base := `
		select t.id, t.category, t.status, t.name, t.numFiles, t.size, t.seeders, t.leechers, t.username, t.added, t.description, t.imdb, t.language, t.textLanguage, t.infoHash, coalesce(c.name, '')`
	if strings.TrimSpace(q) != "" {
		base += `, bm25(torrent_fts) as rank`
	}
	base += `
		from torrents t`
	args := make([]any, 0, 6)
	if strings.TrimSpace(q) != "" {
		base += `
		join torrent_fts on torrent_fts.rowid = t.id and torrent_fts match ?`
		args = append(args, ftsQuery(q))
	}
	base += `
		left join categories c on c.id = t.category`
	where := []string{`(? = '' or t.category = ?)`}
	args = append(args, category, category)
	base += ` where ` + strings.Join(where, ` and `)
	if strings.TrimSpace(q) != "" {
		base += ` order by rank asc, ` + orderBy + ``
	} else {
		base += ` order by ` + orderBy
	}
	base += ` limit ? offset ?`
	args = append(args, limit, offset)

	rows, err := c.db.Query(base, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Torrent
	for rows.Next() {
		var item Torrent
		if strings.TrimSpace(q) != "" {
			var rank float64
			if err := rows.Scan(&item.ID, &item.Category, &item.Status, &item.Name, &item.NumFiles, &item.Size, &item.Seeders, &item.Leechers, &item.Username, &item.Added, &item.Description, &item.IMDB, &item.Language, &item.TextLanguage, &item.InfoHash, &item.CategoryName, &rank); err != nil {
				return nil, err
			}
		} else {
			if err := rows.Scan(&item.ID, &item.Category, &item.Status, &item.Name, &item.NumFiles, &item.Size, &item.Seeders, &item.Leechers, &item.Username, &item.Added, &item.Description, &item.IMDB, &item.Language, &item.TextLanguage, &item.InfoHash, &item.CategoryName); err != nil {
				return nil, err
			}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func ensureFTSIndex(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", path))
	if err != nil {
		return err
	}
	defer db.Close()
	definition := ""
	if row := db.QueryRow(`select coalesce(sql, '') from sqlite_master where type='table' and name='torrent_fts'`); row != nil {
		_ = row.Scan(&definition)
	}
	if strings.Contains(definition, "content='torrents'") {
		if _, err := db.Exec(`drop table if exists torrent_fts`); err != nil {
			return err
		}
		definition = ""
	}
	if definition == "" {
		if _, err := db.Exec(`create virtual table torrent_fts using fts5(name, description, tokenize='unicode61')`); err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`insert into torrent_fts(rowid, name, description) select id, coalesce(name, ''), coalesce(description, '') from torrents`); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return nil
	}
	var hasRow int
	if err := db.QueryRow(`select exists(select 1 from torrent_fts limit 1)`).Scan(&hasRow); err != nil {
		return err
	}
	if hasRow == 0 {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`insert into torrent_fts(rowid, name, description) select id, coalesce(name, ''), coalesce(description, '') from torrents`); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func ftsQuery(q string) string {
	fields := strings.Fields(strings.ToLower(q))
	if len(fields) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.ReplaceAll(field, "\"", "")
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		parts = append(parts, field+"*")
	}
	return strings.Join(parts, " AND ")
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
