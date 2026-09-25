package app

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"who-pirates-the-pirates/internal/catalog"
	"who-pirates-the-pirates/internal/importer"
	"who-pirates-the-pirates/internal/state"
)

type App struct {
	catalog       *catalog.Catalog
	state         *state.Store
	adminPass     string
	secret        []byte
	secureCookies bool
	loginAttempts map[string]loginAttempt
	loginMu       sync.Mutex
	embedded      map[string]*template.Template
}

const maxRequestBodyBytes int64 = 1 << 20

const (
	maxLoginFailures       = 5
	loginWindow            = 15 * time.Minute
	maxTrackedLoginClients = 1024
)

type loginAttempt struct {
	failures int
	resetAt  time.Time
	lastSeen time.Time
}

type AdminPageData struct {
	state.AdminSettings
	ImportSources   []state.ImportSource
	ImportRuns      []state.ImportRun
	ImportManifests []state.ImportManifest
	AuditEntries    []state.AuditEntry
}

//go:embed templates/*.html
var templateFS embed.FS

// templateDir is a test-only override. Production rendering uses templateFS
// so the server remains deployable as a single binary.
var templateDir string

func New(dbPath, statePath string) (*App, error) {
	return NewWithOptions(dbPath, statePath, Options{
		SecureCookies: envEnabled(os.Getenv("APP_COOKIE_SECURE")),
	})
}

type Options struct {
	SecureCookies bool
}

func NewWithOptions(dbPath, statePath string, options Options) (*App, error) {
	db, err := catalog.Open(dbPath)
	if err != nil {
		return nil, err
	}
	st, err := state.Open(statePath)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	secret := []byte(os.Getenv("ADMIN_SESSION_SECRET"))
	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			_ = db.Close()
			_ = st.Close()
			return nil, err
		}
	}
	embedded, err := loadEmbeddedTemplates()
	if err != nil {
		_ = db.Close()
		_ = st.Close()
		return nil, err
	}

	return &App{
		catalog:       db,
		state:         st,
		adminPass:     os.Getenv("ADMIN_PASSWORD"),
		secret:        secret,
		secureCookies: options.SecureCookies,
		loginAttempts: make(map[string]loginAttempt),
		embedded:      embedded,
	}, nil
}

func (a *App) Close() error {
	if a == nil {
		return nil
	}
	var errs []string
	if a.catalog != nil {
		if err := a.catalog.Close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if a.state != nil {
		if err := a.state.Close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (a *App) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleIndex)
	mux.HandleFunc("/torrent/", a.handleTorrentPage)
	mux.HandleFunc("/admin", a.handleAdminPage)
	mux.HandleFunc("/healthz", a.handleHealthz)
	mux.HandleFunc("/api/stats", a.handleStats)
	mux.HandleFunc("/api/categories", a.handleCategories)
	mux.HandleFunc("/api/search", a.handleSearch)
	mux.HandleFunc("/api/torrents/", a.handleTorrentDetail)
	mux.HandleFunc("/api/admin/login", a.handleAdminLogin)
	mux.HandleFunc("/api/admin/logout", a.requireAdmin(a.handleAdminLogout))
	mux.HandleFunc("/api/admin/status", a.requireAdmin(a.handleAdminStatus))
	mux.HandleFunc("/api/admin/audits", a.requireAdmin(a.handleAdminAudits))
	mux.HandleFunc("/api/admin/audits/detail", a.requireAdmin(a.handleAdminAuditDetail))
	mux.HandleFunc("/api/admin/audits/delete", a.requireAdmin(a.handleAdminDeleteAuditEntry))
	mux.HandleFunc("/api/admin/audits/export", a.requireAdmin(a.handleAdminExportAudits))
	mux.HandleFunc("/api/admin/import-manifests/list", a.requireAdmin(a.handleAdminImportManifestsList))
	mux.HandleFunc("/api/admin/import-manifests/detail", a.requireAdmin(a.handleAdminImportManifestDetail))
	mux.HandleFunc("/api/admin/import-manifests/delete", a.requireAdmin(a.handleAdminDeleteImportManifest))
	mux.HandleFunc("/api/admin/import-manifests/export", a.requireAdmin(a.handleAdminExportImportManifests))
	mux.HandleFunc("/api/admin/import-runs/list", a.requireAdmin(a.handleAdminImportRunsList))
	mux.HandleFunc("/api/admin/import-runs/detail", a.requireAdmin(a.handleAdminImportRunDetail))
	mux.HandleFunc("/api/admin/import-runs/export", a.requireAdmin(a.handleAdminExportImportRuns))
	mux.HandleFunc("/api/admin/import-runs/delete", a.requireAdmin(a.handleAdminDeleteImportRun))
	mux.HandleFunc("/api/admin/import-sources/list", a.requireAdmin(a.handleAdminImportSourcesList))
	mux.HandleFunc("/api/admin/import-sources/detail", a.requireAdmin(a.handleAdminImportSourceDetail))
	mux.HandleFunc("/api/admin/import-sources/export", a.requireAdmin(a.handleAdminExportImportSources))
	mux.HandleFunc("/api/admin/import-sources", a.requireAdmin(a.handleAdminImportSources))
	mux.HandleFunc("/api/admin/import-sources/toggle", a.requireAdmin(a.handleAdminToggleImportSource))
	mux.HandleFunc("/api/admin/import-sources/update", a.requireAdmin(a.handleAdminUpdateImportSource))
	mux.HandleFunc("/api/admin/import-sources/delete", a.requireAdmin(a.handleAdminDeleteImportSource))
	mux.HandleFunc("/api/admin/import-runs", a.requireAdmin(a.handleAdminCreateImportRun))
	mux.HandleFunc("/api/admin/import-runs/finish", a.requireAdmin(a.handleAdminFinishImportRun))
	mux.HandleFunc("/api/admin/import-validate", a.requireAdmin(a.handleAdminValidateImportManifest))
	mux.HandleFunc("/api/admin/import-manifest/preview", a.requireAdmin(a.handleAdminPreviewValidatedManifest))
	mux.HandleFunc("/api/admin/import-manifest/record", a.requireAdmin(a.handleAdminRecordValidatedManifest))
	mux.HandleFunc("/api/admin/tor", a.requireAdmin(a.handleAdminTorUpdate))
	return securityHeaders(http.MaxBytesHandler(mux, maxRequestBodyBytes))
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if err := a.renderPage(w, "index", "index.html", nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (a *App) handleTorrentPage(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/torrent/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	torrent, files, _, err := a.loadTorrentPageData(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	magnet := magnetLink(torrent.InfoHash, torrent.Name)
	var magnetURL template.URL
	if magnet != "" {
		// The link is constructed from a validated info hash and an escaped
		// display name; mark it safe so html/template does not replace the
		// magnet: scheme with #ZgotmplZ.
		magnetURL = template.URL(magnet)
	}
	if err := a.renderPage(w, "torrent", "torrent.html", map[string]any{
		"Name":         torrent.Name,
		"CategoryName": torrent.CategoryName,
		"SizeHuman":    formatBytes(torrent.Size),
		"Seeders":      torrent.Seeders,
		"Leechers":     torrent.Leechers,
		"AddedHuman":   formatUnix(torrent.Added),
		"Description":  cleanText(deref(torrent.Description)),
		"Summary":      summarizeText(cleanText(deref(torrent.Description)), 280),
		"Username":     torrent.Username,
		"InfoHash":     torrent.InfoHash,
		"Magnet":       magnetURL,
		"Language":     deref(torrent.Language),
		"IMDB":         deref(torrent.IMDB),
		"Files":        files,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (a *App) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if !a.isAdmin(r) {
		if err := a.renderPage(w, "admin_login", "admin_login.html", nil); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	s, err := a.state.GetAdminSettings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sources, err := a.state.ImportSources()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	runs, err := a.state.ImportRuns(10)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	manifests, err := a.state.ImportManifests(10)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	audits, err := a.state.AuditEntries(10)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := a.renderPage(w, "admin", "admin.html", AdminPageData{AdminSettings: s, ImportSources: sources, ImportRuns: runs, ImportManifests: manifests, AuditEntries: audits}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func templatePath(name string) string {
	if templateDir == "" {
		return filepath.Join("templates", name)
	}
	return filepath.Join(templateDir, name)
}

func loadEmbeddedTemplates() (map[string]*template.Template, error) {
	pages := []string{"index.html", "torrent.html", "admin.html", "admin_login.html"}
	loaded := make(map[string]*template.Template, len(pages))
	for _, page := range pages {
		builder := template.New(page).Funcs(template.FuncMap{
			"dict":     dict,
			"list":     list,
			"urlquery": url.QueryEscape,
		})
		tpl, err := builder.ParseFS(templateFS, "templates/"+page, "templates/base.html")
		if err != nil {
			return nil, err
		}
		loaded[page] = tpl
	}
	return loaded, nil
}

func (a *App) renderPage(w http.ResponseWriter, name, page string, data any) error {
	if templateDir == "" {
		tpl, ok := a.embedded[page]
		if !ok {
			return fmt.Errorf("embedded template %q is not loaded", page)
		}
		return tpl.ExecuteTemplate(w, "base", data)
	}

	builder := template.New(name).Funcs(template.FuncMap{
		"dict":     dict,
		"list":     list,
		"urlquery": url.QueryEscape,
	})
	tpl, err := builder.ParseFiles(templatePath(page), templatePath("base.html"))
	if err != nil {
		return err
	}
	return tpl.ExecuteTemplate(w, "base", data)
}

func (a *App) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if a.catalog.Healthy() != nil || a.state.Healthy() != nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (a *App) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := a.catalog.Stats()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, stats)
}

func (a *App) handleCategories(w http.ResponseWriter, r *http.Request) {
	items, err := a.catalog.Categories()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"items": items})
}

func (a *App) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	sortField := strings.TrimSpace(r.URL.Query().Get("sort"))
	sortDir := strings.TrimSpace(r.URL.Query().Get("dir"))
	limit := clampInt(queryInt(r, "limit", 100), 1, 100)
	offset := clampInt(queryInt(r, "offset", 0), 0, 1000000)
	items, total, err := a.catalog.SearchPage(q, category, sortField, sortDir, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	hasMore := int64(offset)+int64(len(items)) < total
	nextOffset := offset + len(items)
	if !hasMore {
		nextOffset = offset
	}
	writeJSON(w, map[string]any{"items": items, "limit": limit, "offset": offset, "total": total, "hasMore": hasMore, "query": q, "category": category, "sort": sortField, "dir": sortDir, "nextOffset": nextOffset})
}

func (a *App) handleTorrentDetail(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/torrents/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	torrent, files, _, err := a.loadTorrentPageData(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"torrent": torrent, "files": files})
}

func (a *App) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.adminPass == "" {
		http.Error(w, "admin password not configured", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	client := clientAddress(r)
	if a.loginRateLimited(client) {
		_ = a.state.Audit("admin_login_rate_limited", "client="+client)
		http.Error(w, "too many login attempts; try again later", http.StatusTooManyRequests)
		return
	}
	if !subtleConstantTime([]byte(r.FormValue("password")), []byte(a.adminPass)) {
		a.recordLoginFailure(client)
		_ = a.state.Audit("admin_login_failed", "client="+client)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	a.clearLoginFailures(client)
	settings, err := a.state.GetAdminSettings()
	if err != nil {
		http.Error(w, "unable to establish session", http.StatusInternalServerError)
		return
	}
	token, err := a.signSession("admin", settings.AdminSessionEpoch)
	if err != nil {
		http.Error(w, "unable to establish session", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "admin_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: a.secureCookies || r.TLS != nil, MaxAge: 60 * 60 * 8})
	_ = a.state.Audit("admin_login", "session established")
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *App) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := a.state.BumpAdminSessionEpoch(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = a.state.Audit("admin_logout", "session revoked")
	http.SetCookie(w, &http.Cookie{Name: "admin_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: a.secureCookies || r.TLS != nil})
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *App) handleAdminStatus(w http.ResponseWriter, r *http.Request) {
	s, err := a.state.GetAdminSettings()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	sources, err := a.state.ImportSources()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	sourceCount, err := a.state.ImportSourceTotals()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	catalogHealthy := a.catalog.Healthy() == nil
	stateHealthy := a.state.Healthy() == nil
	runs, err := a.state.ImportRuns(10)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	runCount, err := a.state.ImportRunTotals()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	manifests, err := a.state.ImportManifests(10)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	manifestCount, manifestBytes, err := a.state.ImportManifestTotals()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	audits, err := a.state.AuditEntries(10)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var lastLoginAt any = nil
	lastLogin, err := a.state.LatestAuditByAction("admin_login")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, err.Error(), 500)
		return
	}
	if err == nil {
		lastLoginAt = lastLogin.CreatedAt
	}
	var lastLogoutAt any = nil
	lastLogout, err := a.state.LatestAuditByAction("admin_logout")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, err.Error(), 500)
		return
	}
	if err == nil {
		lastLogoutAt = lastLogout.CreatedAt
	}
	auditCount, err := a.state.AuditTotals()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{
		"settings":                 s,
		"adminSessionEpoch":        s.AdminSessionEpoch,
		"catalogHealthy":           catalogHealthy,
		"stateHealthy":             stateHealthy,
		"importSources":            sources,
		"importSourceCount":        sourceCount,
		"importRuns":               runs,
		"importRunCount":           runCount,
		"importManifests":          manifests,
		"importManifestCount":      len(manifests),
		"importManifestTotalCount": manifestCount,
		"importManifestTotalBytes": manifestBytes,
		"auditCount":               auditCount,
		"lastLoginAt":              lastLoginAt,
		"lastLogoutAt":             lastLogoutAt,
		"audits":                   audits,
	})
}

func (a *App) handleAdminImportRunsList(w http.ResponseWriter, r *http.Request) {
	limit := clampInt(queryInt(r, "limit", 25), 1, 100)
	offset := clampInt(queryInt(r, "offset", 0), 0, 1_000_000)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	runs, err := a.state.ImportRunsSearchOffset(q, limit+1, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hasMore := len(runs) > limit
	if hasMore {
		runs = runs[:limit]
	}
	writeJSON(w, map[string]any{
		"items":   runs,
		"limit":   limit,
		"offset":  offset,
		"q":       q,
		"hasMore": hasMore,
		"nextOffset": func() int {
			if hasMore {
				return offset + limit
			}
			return offset
		}(),
	})
}

func (a *App) handleAdminImportRunDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	item, err := a.state.ImportRun(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"run": item})
}

func (a *App) handleAdminExportImportRuns(w http.ResponseWriter, r *http.Request) {
	limit := clampInt(queryInt(r, "limit", 1000), 1, 1000)
	offset := clampInt(queryInt(r, "offset", 0), 0, 1_000_000)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	download := strings.TrimSpace(r.URL.Query().Get("download")) == "1"
	runs, err := a.state.ImportRunsSearchOffset(q, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if download {
		name := "import-runs.json"
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(name))
	}
	_ = a.state.Audit("import_run_export", fmt.Sprintf("count=%d download=%t q=%s offset=%d limit=%d", len(runs), download, q, offset, limit))
	writeJSON(w, map[string]any{
		"items":    runs,
		"q":        q,
		"limit":    limit,
		"offset":   offset,
		"download": download,
		"count":    len(runs),
	})
}

func (a *App) handleAdminDeleteImportRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	item, err := a.state.ImportRun(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := a.state.DeleteImportRun(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = a.state.Audit("import_run_delete", fmt.Sprintf("id=%d status=%s", id, item.Status))
	writeJSON(w, map[string]any{"deleted": true, "id": id})
}

func (a *App) handleAdminImportManifestsList(w http.ResponseWriter, r *http.Request) {
	limit := clampInt(queryInt(r, "limit", 25), 1, 100)
	offset := clampInt(queryInt(r, "offset", 0), 0, 1_000_000)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	manifests, err := a.state.ImportManifestsSearchOffset(q, limit+1, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hasMore := len(manifests) > limit
	if hasMore {
		manifests = manifests[:limit]
	}
	writeJSON(w, map[string]any{
		"items":   manifests,
		"limit":   limit,
		"offset":  offset,
		"q":       q,
		"hasMore": hasMore,
		"nextOffset": func() int {
			if hasMore {
				return offset + limit
			}
			return offset
		}(),
	})
}

func (a *App) handleAdminImportManifestDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	item, err := a.state.ImportManifest(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"manifest": item})
}

func (a *App) handleAdminDeleteImportManifest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	item, err := a.state.ImportManifest(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := a.state.DeleteImportManifest(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = a.state.Audit("import_manifest_delete", fmt.Sprintf("id=%d name=%s", id, item.Name))
	writeJSON(w, map[string]any{"deleted": true, "id": id})
}

func (a *App) handleAdminExportImportManifests(w http.ResponseWriter, r *http.Request) {
	limit := clampInt(queryInt(r, "limit", 1000), 1, 1000)
	offset := clampInt(queryInt(r, "offset", 0), 0, 1_000_000)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	download := strings.TrimSpace(r.URL.Query().Get("download")) == "1"
	manifests, err := a.state.ImportManifestsSearchOffset(q, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var totalBytes int64
	for _, item := range manifests {
		totalBytes += item.TotalBytes
	}
	if download {
		name := "import-manifests.json"
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(name))
	}
	_ = a.state.Audit("import_manifest_export", fmt.Sprintf("count=%d total_bytes=%d download=%t q=%s offset=%d limit=%d", len(manifests), totalBytes, download, q, offset, limit))
	writeJSON(w, map[string]any{
		"items":      manifests,
		"q":          q,
		"limit":      limit,
		"offset":     offset,
		"download":   download,
		"count":      len(manifests),
		"totalBytes": totalBytes,
	})
}

func (a *App) handleAdminAudits(w http.ResponseWriter, r *http.Request) {
	limit := clampInt(queryInt(r, "limit", 25), 1, 100)
	offset := clampInt(queryInt(r, "offset", 0), 0, 1_000_000)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	audits, err := a.state.AuditEntriesSearchOffset(q, limit+1, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hasMore := len(audits) > limit
	if hasMore {
		audits = audits[:limit]
	}
	writeJSON(w, map[string]any{
		"items":   audits,
		"limit":   limit,
		"offset":  offset,
		"q":       q,
		"hasMore": hasMore,
		"nextLimit": func() int {
			if hasMore {
				return limit + limit
			}
			return limit
		}(),
		"nextOffset": func() int {
			if hasMore {
				return offset + limit
			}
			return offset
		}(),
	})
}

func (a *App) handleAdminAuditDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	item, err := a.state.AuditEntry(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"audit": item})
}

func (a *App) handleAdminDeleteAuditEntry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	item, err := a.state.AuditEntry(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := a.state.DeleteAuditEntry(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = a.state.Audit("audit_delete", fmt.Sprintf("id=%d action=%s", id, item.Action))
	writeJSON(w, map[string]any{"deleted": true, "id": id})
}

func (a *App) handleAdminExportAudits(w http.ResponseWriter, r *http.Request) {
	limit := clampInt(queryInt(r, "limit", 1000), 1, 1000)
	offset := clampInt(queryInt(r, "offset", 0), 0, 1_000_000)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	download := strings.TrimSpace(r.URL.Query().Get("download")) == "1"
	items, err := a.state.AuditEntriesSearchOffset(q, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if download {
		name := "audits.json"
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(name))
	}
	_ = a.state.Audit("audit_export", fmt.Sprintf("count=%d download=%t q=%s offset=%d limit=%d", len(items), download, q, offset, limit))
	writeJSON(w, map[string]any{
		"items":    items,
		"q":        q,
		"limit":    limit,
		"offset":   offset,
		"download": download,
		"count":    len(items),
	})
}

func (a *App) handleAdminImportSources(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		kind := strings.TrimSpace(r.FormValue("kind"))
		location := strings.TrimSpace(r.FormValue("location"))
		if name == "" || kind == "" || location == "" {
			http.Error(w, "missing required fields", http.StatusBadRequest)
			return
		}
		enabled := r.FormValue("enabled") != ""
		if err := a.state.CreateImportSource(name, kind, location, enabled); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = a.state.Audit("import_source_create", name+"|"+kind)
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	case http.MethodGet:
		sources, err := a.state.ImportSources()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"items": sources})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) handleAdminImportSourcesList(w http.ResponseWriter, r *http.Request) {
	limit := clampInt(queryInt(r, "limit", 25), 1, 100)
	offset := clampInt(queryInt(r, "offset", 0), 0, 1_000_000)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	sources, err := a.state.ImportSourcesSearchOffset(q, limit+1, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hasMore := len(sources) > limit
	if hasMore {
		sources = sources[:limit]
	}
	writeJSON(w, map[string]any{
		"items":   sources,
		"limit":   limit,
		"offset":  offset,
		"q":       q,
		"hasMore": hasMore,
		"nextOffset": func() int {
			if hasMore {
				return offset + limit
			}
			return offset
		}(),
	})
}

func (a *App) handleAdminImportSourceDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	item, err := a.state.ImportSource(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"source": item})
}

func (a *App) handleAdminExportImportSources(w http.ResponseWriter, r *http.Request) {
	limit := clampInt(queryInt(r, "limit", 1000), 1, 1000)
	offset := clampInt(queryInt(r, "offset", 0), 0, 1_000_000)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	download := strings.TrimSpace(r.URL.Query().Get("download")) == "1"
	sources, err := a.state.ImportSourcesSearchOffset(q, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if download {
		name := "import-sources.json"
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(name))
	}
	_ = a.state.Audit("import_source_export", fmt.Sprintf("count=%d download=%t q=%s offset=%d limit=%d", len(sources), download, q, offset, limit))
	writeJSON(w, map[string]any{
		"items":    sources,
		"q":        q,
		"limit":    limit,
		"offset":   offset,
		"download": download,
		"count":    len(sources),
	})
}

func (a *App) handleAdminToggleImportSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	enabled := r.FormValue("enabled") == "1"
	if err := a.state.SetImportSourceEnabled(id, enabled); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = a.state.Audit("import_source_toggle", fmt.Sprintf("id=%d enabled=%t", id, enabled))
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *App) handleAdminUpdateImportSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	kind := strings.TrimSpace(r.FormValue("kind"))
	location := strings.TrimSpace(r.FormValue("location"))
	if name == "" || kind == "" || location == "" {
		http.Error(w, "missing required fields", http.StatusBadRequest)
		return
	}
	if err := a.state.UpdateImportSource(id, name, kind, location); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = a.state.Audit("import_source_update", fmt.Sprintf("id=%d", id))
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *App) handleAdminDeleteImportSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	item, err := a.state.ImportSource(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := a.state.DeleteImportSource(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = a.state.Audit("import_source_delete", fmt.Sprintf("id=%d name=%s", id, item.Name))
	writeJSON(w, map[string]any{"deleted": true, "id": id})
}

func (a *App) handleAdminCreateImportRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	sourceID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("source_id")), 10, 64)
	if err != nil || sourceID <= 0 {
		http.Error(w, "invalid source id", http.StatusBadRequest)
		return
	}
	status := strings.TrimSpace(r.FormValue("status"))
	if status == "" {
		status = "queued"
	}
	message := strings.TrimSpace(r.FormValue("message"))
	checksum := strings.TrimSpace(r.FormValue("checksum"))
	approvedRef := strings.TrimSpace(r.FormValue("approved_ref"))
	if err := a.state.CreateImportRun(sourceID, status, message, checksum, approvedRef); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = a.state.Audit("import_run_create", fmt.Sprintf("source_id=%d status=%s", sourceID, status))
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *App) handleAdminFinishImportRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	status := strings.TrimSpace(r.FormValue("status"))
	if status == "" {
		status = "finished"
	}
	message := strings.TrimSpace(r.FormValue("message"))
	if err := a.state.FinishImportRun(id, status, message); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = a.state.Audit("import_run_finish", fmt.Sprintf("id=%d status=%s", id, status))
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *App) handleAdminValidateImportManifest(w http.ResponseWriter, r *http.Request) {
	result, approvedBy, err := a.validateManifestRequest(r)
	if err != nil {
		if errors.Is(err, errManifestRejected) {
			_ = a.state.Audit("import_manifest_rejected", err.Error())
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = a.state.Audit("import_manifest_validated", fmt.Sprintf("%s|%s|%d", result.Name, approvedBy, result.TotalBytes))
	writeJSON(w, map[string]any{
		"manifest": map[string]any{
			"name":            result.Name,
			"approvedBy":      approvedBy,
			"files":           result.Files,
			"checksum":        result.Checksum,
			"previewChecksum": result.PreviewChecksum,
			"totalBytes":      result.TotalBytes,
		},
	})
}

func (a *App) handleAdminPreviewValidatedManifest(w http.ResponseWriter, r *http.Request) {
	result, approvedBy, err := a.validateManifestRequest(r)
	if err != nil {
		if errors.Is(err, errManifestRejected) {
			_ = a.state.Audit("import_manifest_rejected", err.Error())
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = a.state.Audit("import_manifest_preview", fmt.Sprintf("%s|%s|%d", result.Name, approvedBy, result.TotalBytes))
	writeJSON(w, map[string]any{
		"manifest": map[string]any{
			"name":            result.Name,
			"approvedBy":      approvedBy,
			"files":           result.Files,
			"checksum":        result.Checksum,
			"previewChecksum": result.PreviewChecksum,
			"totalBytes":      result.TotalBytes,
		},
	})
}

func (a *App) handleAdminRecordValidatedManifest(w http.ResponseWriter, r *http.Request) {
	result, approvedBy, err := a.validateManifestRequest(r)
	if err != nil {
		if errors.Is(err, errManifestRejected) {
			_ = a.state.Audit("import_manifest_rejected", err.Error())
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	sourceID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("source_id")), 10, 64)
	if err != nil || sourceID <= 0 {
		http.Error(w, "invalid source id", http.StatusBadRequest)
		return
	}
	runStatus := strings.TrimSpace(r.FormValue("status"))
	if runStatus == "" {
		runStatus = "queued"
	}
	message := strings.TrimSpace(r.FormValue("message"))
	if message == "" {
		message = "validated manifest recorded from admin"
	}
	approvedRef := strings.TrimSpace(r.FormValue("approved_ref"))
	if approvedRef == "" {
		approvedRef = result.Name
	}
	if err := a.state.RecordValidatedManifest(sourceID, runStatus, message, approvedRef, result.Name, approvedBy, strings.TrimSpace(r.FormValue("base_dir")), result.Checksum, result.PreviewChecksum, result.TotalBytes); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = a.state.Audit("import_manifest_recorded", fmt.Sprintf("source_id=%d name=%s", sourceID, result.Name))
	writeJSON(w, map[string]any{
		"manifest": map[string]any{
			"name":            result.Name,
			"approvedBy":      approvedBy,
			"files":           result.Files,
			"checksum":        result.Checksum,
			"previewChecksum": result.PreviewChecksum,
			"totalBytes":      result.TotalBytes,
		},
		"run": map[string]any{
			"sourceId":    sourceID,
			"status":      runStatus,
			"message":     message,
			"approvedRef": approvedRef,
		},
		"stored": map[string]any{
			"name":            result.Name,
			"approvedBy":      approvedBy,
			"baseDir":         strings.TrimSpace(r.FormValue("base_dir")),
			"checksum":        result.Checksum,
			"previewChecksum": result.PreviewChecksum,
			"totalBytes":      result.TotalBytes,
		},
	})
}

var errManifestRejected = errors.New("manifest rejected")

func (a *App) validateManifestRequest(r *http.Request) (importer.ValidationResult, string, error) {
	if r.Method != http.MethodPost {
		return importer.ValidationResult{}, "", fmt.Errorf("method not allowed")
	}
	if err := r.ParseForm(); err != nil {
		return importer.ValidationResult{}, "", fmt.Errorf("invalid form")
	}
	name := strings.TrimSpace(r.FormValue("name"))
	approvedBy := strings.TrimSpace(r.FormValue("approved_by"))
	checksum := strings.TrimSpace(r.FormValue("checksum"))
	baseDir := strings.TrimSpace(r.FormValue("base_dir"))
	files := splitLines(r.FormValue("files"))
	if name == "" || len(files) == 0 {
		return importer.ValidationResult{}, "", errManifestRejected
	}
	result, err := importer.ValidateManifest(importer.Manifest{
		Name:       name,
		ApprovedBy: approvedBy,
		Checksum:   checksum,
		Files:      files,
	}, baseDir)
	if err != nil {
		return importer.ValidationResult{}, "", errManifestRejected
	}
	return result, approvedBy, nil
}

func (a *App) handleAdminTorUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	settings, err := a.state.GetAdminSettings()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	settings.TorEnabled = r.FormValue("tor_enabled") == "1"
	settings.TorMode = strings.TrimSpace(r.FormValue("tor_mode"))
	settings.TorAutostart = r.FormValue("tor_autostart") == "1"
	settings.OnionAddress = strings.TrimSpace(r.FormValue("onion_address"))
	settings.TorControlAddr = strings.TrimSpace(r.FormValue("tor_control_addr"))
	settings.TorStatus = "configured"
	if settings.TorMode == "" {
		settings.TorMode = "off"
	}
	if !validTorMode(settings.TorMode) {
		http.Error(w, "invalid tor mode", http.StatusBadRequest)
		return
	}
	if err := a.state.PutAdminSettings(settings); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = a.state.Audit("tor_update", "settings updated")
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *App) loadTorrentPageData(id int64) (catalog.Torrent, []catalog.TorrentFile, string, error) {
	t, categoryName, err := a.catalog.Torrent(id)
	if err != nil {
		return catalog.Torrent{}, nil, "", err
	}
	files, err := a.catalog.TorrentFiles(id, 500)
	if err != nil {
		return t, nil, categoryName, err
	}
	return t, files, categoryName, nil
}

func (a *App) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.isAdmin(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if isUnsafeMethod(r.Method) && !sameOrigin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; script-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		if r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/api/admin/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// sameOrigin rejects cross-origin browser requests while allowing non-browser
// clients that do not send Origin or Referer headers.
func sameOrigin(r *http.Request) bool {
	value := r.Header.Get("Origin")
	if value == "" {
		value = r.Referer()
	}
	if value == "" {
		return true
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host != r.Host {
		return false
	}
	requestScheme := "http"
	if r.TLS != nil {
		requestScheme = "https"
	}
	return u.Scheme == requestScheme
}

func (a *App) isAdmin(r *http.Request) bool {
	c, err := r.Cookie("admin_session")
	if err != nil || c.Value == "" {
		return false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 2 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	mac, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	fields := strings.Split(string(payload), ":")
	if len(fields) != 3 || fields[0] != "admin" {
		return false
	}
	epoch, err := strconv.ParseInt(fields[1], 10, 64)
	issuedAt, issuedErr := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || issuedErr != nil || issuedAt > time.Now().Add(time.Minute).Unix() || time.Since(time.Unix(issuedAt, 0)) > 8*time.Hour {
		return false
	}
	settings, err := a.state.GetAdminSettings()
	if err != nil || settings.AdminSessionEpoch != epoch {
		return false
	}
	sum := hmac.New(sha256.New, a.secret)
	sum.Write(payload)
	return hmac.Equal(mac, sum.Sum(nil))
}

func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func (a *App) loginRateLimited(client string) bool {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	a.pruneLoginAttempts(time.Now())
	attempt, ok := a.loginAttempts[client]
	if !ok {
		return false
	}
	if time.Now().After(attempt.resetAt) {
		delete(a.loginAttempts, client)
		return false
	}
	return attempt.failures >= maxLoginFailures
}

func (a *App) recordLoginFailure(client string) {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	now := time.Now()
	a.pruneLoginAttempts(now)
	if _, ok := a.loginAttempts[client]; !ok && len(a.loginAttempts) >= maxTrackedLoginClients {
		a.evictOldestLoginAttempt()
	}
	attempt := a.loginAttempts[client]
	if attempt.resetAt.IsZero() || now.After(attempt.resetAt) {
		attempt = loginAttempt{resetAt: now.Add(loginWindow)}
	}
	attempt.failures++
	attempt.lastSeen = now
	a.loginAttempts[client] = attempt
}

func (a *App) pruneLoginAttempts(now time.Time) {
	for client, attempt := range a.loginAttempts {
		if !attempt.resetAt.IsZero() && now.After(attempt.resetAt) {
			delete(a.loginAttempts, client)
		}
	}
}

func (a *App) evictOldestLoginAttempt() {
	var oldestClient string
	var oldest time.Time
	for client, attempt := range a.loginAttempts {
		if oldestClient == "" || attempt.lastSeen.Before(oldest) {
			oldestClient = client
			oldest = attempt.lastSeen
		}
	}
	if oldestClient != "" {
		delete(a.loginAttempts, oldestClient)
	}
}

func (a *App) clearLoginFailures(client string) {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	delete(a.loginAttempts, client)
}

func (a *App) signSession(subject string, epoch ...int64) (string, error) {
	currentEpoch := int64(0)
	if len(epoch) > 0 {
		currentEpoch = epoch[0]
	}
	payload := []byte(subject + ":" + strconv.FormatInt(currentEpoch, 10) + ":" + strconv.FormatInt(time.Now().Unix(), 10))
	sum := hmac.New(sha256.New, a.secret)
	sum.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(sum.Sum(nil)), nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func queryInt(r *http.Request, key string, def int) int {
	if v := strings.TrimSpace(r.URL.Query().Get(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func clampInt(n, min, max int) int {
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func validTorMode(mode string) bool {
	switch mode {
	case "off", "clearnet", "onion", "dual":
		return true
	default:
		return false
	}
}

func magnetLink(infoHash, name string) string {
	infoHash = strings.TrimSpace(infoHash)
	if !validInfoHash(infoHash) {
		return ""
	}
	kind := "btih"
	value := strings.ToLower(infoHash)
	if len(infoHash) == 32 {
		kind = "btih"
		value = strings.ToUpper(infoHash)
	}
	return "magnet:?xt=urn:" + kind + ":" + value + "&dn=" + url.QueryEscape(name)
}

func validInfoHash(infoHash string) bool {
	if len(infoHash) == 40 {
		for _, r := range infoHash {
			if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') && !(r >= 'A' && r <= 'F') {
				return false
			}
		}
		return true
	}
	if len(infoHash) == 32 {
		for _, r := range infoHash {
			if !(r >= 'A' && r <= 'Z') && !(r >= '2' && r <= '7') {
				return false
			}
		}
		return true
	}
	return false
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

func formatUnix(ts int64) string {
	return time.Unix(ts, 0).UTC().Format("2006-01-02 15:04:05 UTC")
}

func cleanText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

func splitLines(s string) []string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func summarizeText(s string, limit int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "No description provided."
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "..."
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func dict(values ...any) map[string]any {
	m := make(map[string]any, len(values)/2)
	for i := 0; i+1 < len(values); i += 2 {
		key, _ := values[i].(string)
		m[key] = values[i+1]
	}
	return m
}

func list(values ...any) []any {
	return values
}

func subtleConstantTime(a, b []byte) bool {
	// Compare fixed-size SHA-256 digests so callers cannot distinguish
	// password lengths through timing. The digest comparison is constant time
	// even when the supplied secrets have different lengths.
	adigest := sha256.Sum256(a)
	bdigest := sha256.Sum256(b)
	var diff byte
	for i := range adigest {
		diff |= adigest[i] ^ bdigest[i]
	}
	return diff == 0
}

func envEnabled(value string) bool {
	return value == "1" || strings.EqualFold(strings.TrimSpace(value), "true")
}
