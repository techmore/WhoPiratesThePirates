package catalog

import (
	"database/sql"
	"fmt"
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
		select t.id, t.category, t.status, t.name, t.numFiles, t.size, t.seeders, t.leechers, t.username, t.added, t.description, t.imdb, t.language, t.textLanguage, t.infoHash, coalesce(c.name, '')
		from torrents t`
	args := make([]any, 0, 6)
	base += `
		left join categories c on c.id = t.category`
	where := []string{`(? = '' or t.category = ?)`}
	args = append(args, category, category)
	if q = strings.TrimSpace(q); q != "" {
		// LIKE keeps the catalog connection strictly read-only. Any future FTS
		// acceleration belongs in an application-owned derived database.
		where = append(where, `(coalesce(t.name, '') like ? escape '\' or coalesce(t.description, '') like ? escape '\' or coalesce(t.infoHash, '') like ? escape '\')`)
		pattern := "%" + escapeLike(q) + "%"
		args = append(args, pattern, pattern, pattern)
	}
	base += ` where ` + strings.Join(where, ` and `)
	base += ` order by ` + orderBy
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
		if err := rows.Scan(&item.ID, &item.Category, &item.Status, &item.Name, &item.NumFiles, &item.Size, &item.Seeders, &item.Leechers, &item.Username, &item.Added, &item.Description, &item.IMDB, &item.Language, &item.TextLanguage, &item.InfoHash, &item.CategoryName); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
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
