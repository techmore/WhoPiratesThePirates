package app

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"who-pirates-the-pirates/internal/state"

	_ "modernc.org/sqlite"
)

const (
	testAdminPassword      = "secret"
	testAdminSessionSecret = "01234567890123456789012345678901"
)

func TestAdminManifestRecordFlow(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	baseDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseDir, "data.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"source_id":    []string{"1"},
		"name":         []string{"Approved dataset"},
		"approved_by":  []string{"owner"},
		"checksum":     []string{""},
		"base_dir":     []string{baseDir},
		"files":        []string{"data.txt"},
		"approved_ref": []string{"ref/approved"},
		"message":      []string{"validated"},
		"status":       []string{"queued"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/import-manifest/record", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	seedState(t, statePath)
	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Manifest struct {
			Name            string   `json:"name"`
			ApprovedBy      string   `json:"approvedBy"`
			Files           []string `json:"files"`
			PreviewChecksum string   `json:"previewChecksum"`
		} `json:"manifest"`
		Run struct {
			SourceID    int64  `json:"sourceId"`
			Status      string `json:"status"`
			Message     string `json:"message"`
			ApprovedRef string `json:"approvedRef"`
		} `json:"run"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Manifest.Name != "Approved dataset" || payload.Run.Status != "queued" || payload.Run.ApprovedRef != "ref/approved" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	if payload.Manifest.PreviewChecksum == "" {
		t.Fatal("expected preview checksum in response")
	}

	sources, err := a.state.ImportRuns(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected one import run, got %#v", sources)
	}
	if sources[0].ApprovedRef != "ref/approved" || sources[0].Status != "queued" {
		t.Fatalf("unexpected run: %#v", sources[0])
	}

	manifests, err := a.state.ImportManifests(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 1 {
		t.Fatalf("expected one stored manifest, got %#v", manifests)
	}
	if manifests[0].Name != "Approved dataset" || manifests[0].ApprovedBy != "owner" || manifests[0].PreviewChecksum != payload.Manifest.PreviewChecksum {
		t.Fatalf("unexpected manifest record: %#v", manifests[0])
	}
}

func TestAdminLoginAndLogoutFlow(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookie := loginAsAdmin(t, a)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/status", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected protected endpoint after login, got %d body=%s", rec.Code, rec.Body.String())
	}
	loginAudits, err := a.state.AuditEntries(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(loginAudits) == 0 || loginAudits[0].Action != "admin_login" {
		t.Fatalf("expected login audit entry, got %#v", loginAudits)
	}

	torForm := url.Values{
		"tor_enabled":      []string{"1"},
		"tor_mode":         []string{"dual"},
		"tor_autostart":    []string{"1"},
		"onion_address":    []string{"example.onion"},
		"tor_control_addr": []string{"127.0.0.1:9051"},
	}
	torReq := httptest.NewRequest(http.MethodPost, "/api/admin/tor", strings.NewReader(torForm.Encode()))
	torReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	torReq.AddCookie(cookie)
	torRec := httptest.NewRecorder()
	a.Router().ServeHTTP(torRec, torReq)
	if torRec.Code != http.StatusSeeOther {
		t.Fatalf("unexpected tor update code: %d body=%s", torRec.Code, torRec.Body.String())
	}

	postTorReq := httptest.NewRequest(http.MethodGet, "/api/admin/status", nil)
	postTorReq.AddCookie(cookie)
	postTorRec := httptest.NewRecorder()
	a.Router().ServeHTTP(postTorRec, postTorReq)
	if postTorRec.Code != http.StatusOK {
		t.Fatalf("expected session to survive tor update, got %d body=%s", postTorRec.Code, postTorRec.Body.String())
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/api/admin/logout", nil)
	logoutReq.AddCookie(cookie)
	logoutRec := httptest.NewRecorder()
	a.Router().ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusSeeOther {
		t.Fatalf("unexpected logout code: %d body=%s", logoutRec.Code, logoutRec.Body.String())
	}
	if got := logoutRec.Header().Get("Set-Cookie"); !strings.Contains(got, "admin_session=") || !strings.Contains(got, "Path=/") {
		t.Fatalf("expected session clearing cookie, got %q", got)
	}
	audits, err := a.state.AuditEntries(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) == 0 || audits[0].Action != "admin_logout" {
		t.Fatalf("expected logout audit entry, got %#v", audits)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/admin/status", nil)
	statusReq.AddCookie(cookie)
	statusRec := httptest.NewRecorder()
	a.Router().ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected revoked cookie to be rejected, got %d body=%s", statusRec.Code, statusRec.Body.String())
	}
	freshCookie, err := a.signSession("admin", 1)
	if err != nil {
		t.Fatal(err)
	}
	statusReq = httptest.NewRequest(http.MethodGet, "/api/admin/status", nil)
	statusReq.AddCookie(&http.Cookie{Name: "admin_session", Value: freshCookie})
	statusRec = httptest.NewRecorder()
	a.Router().ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("expected fresh cookie to work, got %d body=%s", statusRec.Code, statusRec.Body.String())
	}
	var statusPayload struct {
		LastLoginAt  any `json:"lastLoginAt"`
		LastLogoutAt any `json:"lastLogoutAt"`
	}
	if err := json.Unmarshal(statusRec.Body.Bytes(), &statusPayload); err != nil {
		t.Fatal(err)
	}
	if statusPayload.LastLoginAt == nil {
		t.Fatalf("expected lastLoginAt in status payload, got %#v", statusPayload)
	}
	if statusPayload.LastLogoutAt == nil {
		t.Fatalf("expected lastLogoutAt in status payload, got %#v", statusPayload)
	}

	secondCookie := loginAsAdmin(t, a)
	secondLogoutReq := httptest.NewRequest(http.MethodPost, "/api/admin/logout", nil)
	secondLogoutReq.AddCookie(secondCookie)
	secondLogoutRec := httptest.NewRecorder()
	a.Router().ServeHTTP(secondLogoutRec, secondLogoutReq)
	if secondLogoutRec.Code != http.StatusSeeOther {
		t.Fatalf("unexpected second logout code: %d body=%s", secondLogoutRec.Code, secondLogoutRec.Body.String())
	}
	freshStatusReq := httptest.NewRequest(http.MethodGet, "/api/admin/status", nil)
	freshStatusReq.AddCookie(secondCookie)
	freshStatusRec := httptest.NewRecorder()
	a.Router().ServeHTTP(freshStatusRec, freshStatusReq)
	if freshStatusRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected second revoked cookie to be rejected, got %d body=%s", freshStatusRec.Code, freshStatusRec.Body.String())
	}
	bumpedFreshCookie, err := a.signSession("admin", 2)
	if err != nil {
		t.Fatal(err)
	}
	freshStatusReq = httptest.NewRequest(http.MethodGet, "/api/admin/status", nil)
	freshStatusReq.AddCookie(&http.Cookie{Name: "admin_session", Value: bumpedFreshCookie})
	freshStatusRec = httptest.NewRecorder()
	a.Router().ServeHTTP(freshStatusRec, freshStatusReq)
	if freshStatusRec.Code != http.StatusOK {
		t.Fatalf("expected refreshed cookie after second logout, got %d body=%s", freshStatusRec.Code, freshStatusRec.Body.String())
	}
	var refreshedStatusPayload struct {
		LastLoginAt  any `json:"lastLoginAt"`
		LastLogoutAt any `json:"lastLogoutAt"`
	}
	if err := json.Unmarshal(freshStatusRec.Body.Bytes(), &refreshedStatusPayload); err != nil {
		t.Fatal(err)
	}
	if refreshedStatusPayload.LastLoginAt == nil {
		t.Fatalf("expected lastLoginAt after second login, got %#v", refreshedStatusPayload)
	}
	if refreshedStatusPayload.LastLogoutAt == nil {
		t.Fatalf("expected lastLogoutAt after second logout, got %#v", refreshedStatusPayload)
	}

	postLogoutReq := httptest.NewRequest(http.MethodGet, "/api/admin/status", nil)
	postLogoutReq.AddCookie(cookie)
	postLogoutRec := httptest.NewRecorder()
	a.Router().ServeHTTP(postLogoutRec, postLogoutReq)
	if postLogoutRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected logout to revoke access, got %d body=%s", postLogoutRec.Code, postLogoutRec.Body.String())
	}
}

func TestAdminAccessIsAvailableWithoutPasswordForLocalMode(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()
	a.adminPass = ""

	pageReq := httptest.NewRequest(http.MethodGet, "/admin", nil)
	pageRec := httptest.NewRecorder()
	a.Router().ServeHTTP(pageRec, pageReq)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("expected password-free admin page, got %d body=%s", pageRec.Code, pageRec.Body.String())
	}
	if !strings.Contains(pageRec.Body.String(), "Upload and Publish Recovery Catalog") {
		t.Fatalf("expected upload control in password-free admin page, body=%s", pageRec.Body.String())
	}
	for _, marker := range []string{"catalog-preset", "The Pirate Bay &amp; YTS database backup", "Open-source software and Linux distributions", "Approve and Start Recovery"} {
		if !strings.Contains(pageRec.Body.String(), marker) {
			t.Fatalf("expected catalog preset workflow marker %q, body=%s", marker, pageRec.Body.String())
		}
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/admin/status", nil)
	statusRec := httptest.NewRecorder()
	a.Router().ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("expected password-free admin status, got %d body=%s", statusRec.Code, statusRec.Body.String())
	}
}

func TestAdminTorUpdatePreservesSessionEpoch(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	if _, err := a.state.BumpAdminSessionEpoch(); err != nil {
		t.Fatal(err)
	}
	sessionBefore, err := a.state.GetAdminSettings()
	if err != nil {
		t.Fatal(err)
	}

	cookieVal, err := a.signSession("admin", sessionBefore.AdminSessionEpoch)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"tor_enabled":      []string{"1"},
		"tor_mode":         []string{"dual"},
		"tor_autostart":    []string{"1"},
		"onion_address":    []string{"example.onion"},
		"tor_control_addr": []string{"127.0.0.1:9051"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/tor", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	after, err := a.state.GetAdminSettings()
	if err != nil {
		t.Fatal(err)
	}
	if after.AdminSessionEpoch != sessionBefore.AdminSessionEpoch {
		t.Fatalf("expected tor update to preserve epoch, before=%d after=%d", sessionBefore.AdminSessionEpoch, after.AdminSessionEpoch)
	}
}

func TestAdminTorUpdateRejectsInvalidMode(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookie := loginAsAdmin(t, a)
	form := url.Values{"tor_mode": []string{"invalid"}}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/tor", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid tor mode to be rejected, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminLogoutRejectsGet(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/admin/logout", nil)
	cookie := loginAsAdmin(t, a)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminLogoutRequiresAuthenticatedSession(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/admin/logout", nil)
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated logout to be rejected, got %d body=%s", rec.Code, rec.Body.String())
	}
	settings, err := a.state.GetAdminSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.AdminSessionEpoch != 0 {
		t.Fatalf("unauthenticated logout must not revoke sessions: %#v", settings)
	}
}

func TestRouterRejectsOversizedRequestBodies(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(strings.Repeat("x", int(maxRequestBodyBytes)+1)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected oversized form to be rejected, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminStateChangesRejectCrossOriginRequests(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookie := loginAsAdmin(t, a)
	req := httptest.NewRequest(http.MethodPost, "http://catalog.test/api/admin/logout", nil)
	req.Header.Set("Origin", "https://attacker.example")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected cross-origin logout to be rejected, got %d body=%s", rec.Code, rec.Body.String())
	}
	settings, err := a.state.GetAdminSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.AdminSessionEpoch != 0 {
		t.Fatalf("cross-origin request must not revoke sessions: %#v", settings)
	}
}

func TestSecurityHeadersAndAdminNoStore(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("missing security headers: %#v", rec.Header())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected admin response to disable caching, got %q", rec.Header().Get("Cache-Control"))
	}
}

func TestAdminSessionExpiresServerSide(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	payload := []byte("admin:0:" + strconv.FormatInt(time.Now().Add(-9*time.Hour).Unix(), 10))
	sum := hmac.New(sha256.New, a.secret)
	_, _ = sum.Write(payload)
	token := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(sum.Sum(nil))
	req := httptest.NewRequest(http.MethodGet, "/api/admin/status", nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: token})
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected an expired signed session to be rejected, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminLoginRateLimitAndAudit(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	for range maxLoginFailures {
		form := url.Values{"password": []string{"wrong"}}
		req := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "192.0.2.10:12345"
		rec := httptest.NewRecorder()
		a.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected failed login to be unauthorized, got %d body=%s", rec.Code, rec.Body.String())
		}
	}
	form := url.Values{"password": []string{testAdminPassword}}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "192.0.2.10:54321"
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected rate-limited login, got %d body=%s", rec.Code, rec.Body.String())
	}
	audits, err := a.state.AuditEntriesSearchOffset("admin_login", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	failed, limited := 0, 0
	for _, audit := range audits {
		switch audit.Action {
		case "admin_login_failed":
			failed++
		case "admin_login_rate_limited":
			limited++
		}
	}
	if failed != maxLoginFailures || limited != 1 {
		t.Fatalf("unexpected login audit records: %#v", audits)
	}
}

func TestLoginAttemptTrackingIsBounded(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	for i := 0; i < maxTrackedLoginClients+100; i++ {
		a.recordLoginFailure("client-" + strconv.Itoa(i))
	}
	if got := len(a.loginAttempts); got > maxTrackedLoginClients {
		t.Fatalf("expected at most %d tracked clients, got %d", maxTrackedLoginClients, got)
	}
}

func TestSecureCookieOptionMarksAdminSession(t *testing.T) {
	a, catalogPath, statePath := newTestApp(t, testAdminPassword)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := NewWithOptions(catalogPath, statePath, Options{SecureCookies: true})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	cookie := loginAsAdmin(t, a)
	if !cookie.Secure {
		t.Fatalf("expected secure admin session cookie, got %#v", cookie)
	}
}

func TestAdminLogoutRevocationSurvivesRestart(t *testing.T) {
	a, catalogPath, statePath := newTestApp(t, testAdminPassword)

	cookie := loginAsAdmin(t, a)

	logoutReq := httptest.NewRequest(http.MethodPost, "/api/admin/logout", nil)
	logoutReq.AddCookie(cookie)
	logoutRec := httptest.NewRecorder()
	a.Router().ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusSeeOther {
		t.Fatalf("unexpected logout code: %d body=%s", logoutRec.Code, logoutRec.Body.String())
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	a2, err := New(catalogPath, statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer a2.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/admin/status", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	a2.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected revoked cookie after restart, got %d body=%s", rec.Code, rec.Body.String())
	}

	reloginCookie := loginAsAdmin(t, a2)

	statusReq := httptest.NewRequest(http.MethodGet, "/api/admin/status", nil)
	statusReq.AddCookie(reloginCookie)
	statusRec := httptest.NewRecorder()
	a2.Router().ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("expected status after restart relogin, got %d body=%s", statusRec.Code, statusRec.Body.String())
	}
	var statusPayload struct {
		LastLoginAt  any `json:"lastLoginAt"`
		LastLogoutAt any `json:"lastLogoutAt"`
	}
	if err := json.Unmarshal(statusRec.Body.Bytes(), &statusPayload); err != nil {
		t.Fatal(err)
	}
	if statusPayload.LastLoginAt == nil {
		t.Fatalf("expected lastLoginAt after restart relogin, got %#v", statusPayload)
	}
	if statusPayload.LastLogoutAt == nil {
		t.Fatalf("expected lastLogoutAt after restart relogin, got %#v", statusPayload)
	}
}

func loginAsAdmin(t *testing.T, a *App) *http.Cookie {
	t.Helper()

	loginForm := url.Values{"password": []string{testAdminPassword}}
	loginReq := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(loginForm.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()

	a.Router().ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusSeeOther {
		t.Fatalf("unexpected login code: %d body=%s", loginRec.Code, loginRec.Body.String())
	}
	cookies := loginRec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != "admin_session" || cookies[0].Value == "" {
		t.Fatalf("expected admin session cookie, got %#v", cookies)
	}
	return cookies[0]
}

func TestAdminLoginRejectsBadRequests(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	tests := []struct {
		name   string
		method string
		body   string
		want   int
	}{
		{name: "method", method: http.MethodGet, want: http.StatusMethodNotAllowed},
		{name: "bad password", method: http.MethodPost, body: "password=nope", want: http.StatusUnauthorized},
		{name: "invalid form", method: http.MethodPost, body: "%", want: http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/api/admin/login", strings.NewReader(tc.body))
			if tc.method == http.MethodPost {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			rec := httptest.NewRecorder()

			a.Router().ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}

}

func TestAdminLoginRedirectsWithoutConfiguredPassword(t *testing.T) {
	a, _ := newAdminTestApp(t, "")
	defer a.Close()

	form := url.Values{"password": []string{testAdminPassword}}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin" {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminImportManifestsListEndpointReturnsItems(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.CreateImportManifest("Alpha manifest", "owner", "/tmp", "abc", "abc", 123); err != nil {
		t.Fatal(err)
	}
	if err := a.state.CreateImportManifest("Beta manifest", "owner", "/tmp", "def", "def", 456); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/import-manifests/list?limit=1&offset=0", nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
		Limit      int  `json:"limit"`
		Offset     int  `json:"offset"`
		HasMore    bool `json:"hasMore"`
		NextOffset int  `json:"nextOffset"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Limit != 1 || payload.Offset != 0 || !payload.HasMore || payload.NextOffset != 1 || len(payload.Items) != 1 || payload.Items[0].Name != "Beta manifest" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAdminImportManifestsListEndpointFiltersQuery(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.CreateImportManifest("Alpha manifest", "owner", "/tmp", "abc", "abc", 123); err != nil {
		t.Fatal(err)
	}
	if err := a.state.CreateImportManifest("Beta manifest", "owner", "/tmp", "def", "def", 456); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/import-manifests/list?limit=10&offset=0&q=alp", nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 1 || payload.Items[0].Name != "Alpha manifest" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAdminImportManifestDetailEndpointReturnsItem(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.CreateImportManifest("Alpha manifest", "owner", "/tmp", "abc", "abc", 123); err != nil {
		t.Fatal(err)
	}
	manifests, err := a.state.ImportManifests(10)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/import-manifests/detail?id="+strconv.FormatInt(manifests[0].ID, 10), nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Manifest struct {
			ID      int64  `json:"id"`
			Name    string `json:"name"`
			BaseDir string `json:"baseDir"`
		} `json:"manifest"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Manifest.ID != manifests[0].ID || payload.Manifest.Name != "Alpha manifest" || payload.Manifest.BaseDir != "/tmp" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAdminDeleteImportManifestEndpointDeletesItem(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.CreateImportManifest("Alpha manifest", "owner", "/tmp", "abc", "abc", 123); err != nil {
		t.Fatal(err)
	}
	manifests, err := a.state.ImportManifests(10)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"id": []string{strconv.FormatInt(manifests[0].ID, 10)}}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/import-manifests/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Deleted bool  `json:"deleted"`
		ID      int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Deleted || payload.ID != manifests[0].ID {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	if _, err := a.state.ImportManifest(manifests[0].ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected deleted manifest to be missing, got %v", err)
	}
}

func TestAdminDeleteEndpointsRejectBadRequests(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		method string
		target string
		body   string
		want   int
	}{
		{name: "source method", method: http.MethodGet, target: "/api/admin/import-sources/delete", want: http.StatusMethodNotAllowed},
		{name: "source id", method: http.MethodPost, target: "/api/admin/import-sources/delete", body: "id=bad", want: http.StatusBadRequest},
		{name: "run method", method: http.MethodGet, target: "/api/admin/import-runs/delete", want: http.StatusMethodNotAllowed},
		{name: "run id", method: http.MethodPost, target: "/api/admin/import-runs/delete", body: "id=bad", want: http.StatusBadRequest},
		{name: "audit method", method: http.MethodGet, target: "/api/admin/audits/delete", want: http.StatusMethodNotAllowed},
		{name: "audit id", method: http.MethodPost, target: "/api/admin/audits/delete", body: "id=bad", want: http.StatusBadRequest},
		{name: "manifest method", method: http.MethodGet, target: "/api/admin/import-manifests/delete", want: http.StatusMethodNotAllowed},
		{name: "manifest id", method: http.MethodPost, target: "/api/admin/import-manifests/delete", body: "id=bad", want: http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			if tc.method == http.MethodPost {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
			rec := httptest.NewRecorder()

			a.Router().ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAdminDeleteEndpointsRequireAuth(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	tests := []struct {
		name   string
		method string
		target string
	}{
		{name: "source", method: http.MethodPost, target: "/api/admin/import-sources/delete"},
		{name: "run", method: http.MethodPost, target: "/api/admin/import-runs/delete"},
		{name: "audit", method: http.MethodPost, target: "/api/admin/audits/delete"},
		{name: "manifest", method: http.MethodPost, target: "/api/admin/import-manifests/delete"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader("id=1"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()

			a.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}

}

func TestAdminEndpointsRequireAuth(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	tests := []struct {
		name   string
		method string
		target string
	}{
		{name: "status", method: http.MethodGet, target: "/api/admin/status"},
		{name: "audits", method: http.MethodGet, target: "/api/admin/audits"},
		{name: "audit detail", method: http.MethodGet, target: "/api/admin/audits/detail?id=1"},
		{name: "runs list", method: http.MethodGet, target: "/api/admin/import-runs/list"},
		{name: "runs detail", method: http.MethodGet, target: "/api/admin/import-runs/detail?id=1"},
		{name: "sources list", method: http.MethodGet, target: "/api/admin/import-sources/list"},
		{name: "sources detail", method: http.MethodGet, target: "/api/admin/import-sources/detail?id=1"},
		{name: "manifests list", method: http.MethodGet, target: "/api/admin/import-manifests/list"},
		{name: "manifests detail", method: http.MethodGet, target: "/api/admin/import-manifests/detail?id=1"},
		{name: "manifest preview", method: http.MethodPost, target: "/api/admin/import-manifest/preview"},
		{name: "manifest record", method: http.MethodPost, target: "/api/admin/import-manifest/record"},
		{name: "tor update", method: http.MethodPost, target: "/api/admin/tor"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.target, nil)
			rec := httptest.NewRecorder()

			a.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAdminEndpointsRejectBadSessionCookie(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	tests := []struct {
		name   string
		method string
		target string
	}{
		{name: "status", method: http.MethodGet, target: "/api/admin/status"},
		{name: "audits", method: http.MethodGet, target: "/api/admin/audits"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.target, nil)
			req.AddCookie(&http.Cookie{Name: "admin_session", Value: "bogus"})
			rec := httptest.NewRecorder()

			a.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAdminImportRunDetailEndpointReturnsItem(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.CreateImportSource("Feed", "http", "https://example.com", true); err != nil {
		t.Fatal(err)
	}
	if err := a.state.CreateImportRun(1, "queued", "created for test", "abc123", "approved/ref"); err != nil {
		t.Fatal(err)
	}
	runs, err := a.state.ImportRuns(10)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/import-runs/detail?id="+strconv.FormatInt(runs[0].ID, 10), nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Run struct {
			ID       int64  `json:"id"`
			Status   string `json:"status"`
			SourceID int64  `json:"sourceId"`
		} `json:"run"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Run.ID != runs[0].ID || payload.Run.Status != "queued" || payload.Run.SourceID != 1 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAdminExportImportManifestsEndpointReturnsItems(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.CreateImportManifest("Alpha manifest", "owner", "/tmp", "abc", "abc", 123); err != nil {
		t.Fatal(err)
	}
	if err := a.state.CreateImportManifest("Beta manifest", "owner", "/tmp", "def", "def", 456); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/import-manifests/export", nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Count      int   `json:"count"`
		TotalBytes int64 `json:"totalBytes"`
		Items      []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 2 || payload.TotalBytes != 579 || len(payload.Items) != 2 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	audits, err := a.state.AuditEntries(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) == 0 || audits[0].Action != "import_manifest_export" {
		t.Fatalf("expected export audit entry, got %#v", audits)
	}
}

func TestAdminExportImportManifestsEndpointDownloadsJson(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.CreateImportManifest("Alpha manifest", "owner", "/tmp", "abc", "abc", 123); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/import-manifests/export?download=1&limit=1&offset=0", nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="import-manifests.json"` {
		t.Fatalf("unexpected disposition: %q", got)
	}
	var payload struct {
		Download bool `json:"download"`
		Count    int  `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Download || payload.Count != 1 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	audits, err := a.state.AuditEntries(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) == 0 || audits[0].Action != "import_manifest_export" {
		t.Fatalf("expected export audit entry, got %#v", audits)
	}
}

func TestAdminExportImportRunsEndpointReturnsItems(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.CreateImportSource("Feed", "http", "https://example.com", true); err != nil {
		t.Fatal(err)
	}
	if err := a.state.CreateImportRun(1, "queued", "created for test", "abc123", "approved/ref"); err != nil {
		t.Fatal(err)
	}
	if err := a.state.CreateImportRun(1, "finished", "done", "def456", "approved/ref2"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/import-runs/export", nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Count int `json:"count"`
		Items []struct {
			Status string `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 2 || len(payload.Items) != 2 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAdminExportImportRunsEndpointDownloadsJson(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.CreateImportSource("Feed", "http", "https://example.com", true); err != nil {
		t.Fatal(err)
	}
	if err := a.state.CreateImportRun(1, "queued", "created for test", "abc123", "approved/ref"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/import-runs/export?download=1&limit=1&offset=0", nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="import-runs.json"` {
		t.Fatalf("unexpected disposition: %q", got)
	}
	var payload struct {
		Download bool `json:"download"`
		Count    int  `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Download || payload.Count != 1 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAdminDeleteImportRunEndpointDeletesItem(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.CreateImportRun(1, "queued", "created for test", "abc123", "approved/ref"); err != nil {
		t.Fatal(err)
	}
	runs, err := a.state.ImportRuns(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) == 0 {
		t.Fatal("expected at least one run to delete")
	}

	form := url.Values{"id": []string{strconv.FormatInt(runs[0].ID, 10)}}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/import-runs/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := a.state.ImportRun(runs[0].ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected deleted run to be missing, got %v", err)
	}
}

func TestAdminExportImportManifestsEndpointFiltersQuery(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.CreateImportManifest("Alpha manifest", "owner", "/tmp", "abc", "abc", 123); err != nil {
		t.Fatal(err)
	}
	if err := a.state.CreateImportManifest("Beta manifest", "owner", "/tmp", "def", "def", 456); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/import-manifests/export?limit=1&offset=0&q=alp", nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Count int `json:"count"`
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 1 || payload.Limit != 1 || payload.Offset != 0 || len(payload.Items) != 1 || payload.Items[0].Name != "Alpha manifest" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAdminManifestValidateRejectsTraversal(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"name":        []string{"Bad dataset"},
		"approved_by": []string{"owner"},
		"base_dir":    []string{t.TempDir()},
		"files":       []string{"../escape.txt"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/import-validate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminManifestPreviewReturnsJson(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	baseDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseDir, "data.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"name":        []string{"Preview dataset"},
		"approved_by": []string{"owner"},
		"base_dir":    []string{baseDir},
		"files":       []string{"data.txt"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/import-manifest/preview", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Manifest struct {
			Name            string `json:"name"`
			ApprovedBy      string `json:"approvedBy"`
			PreviewChecksum string `json:"previewChecksum"`
		} `json:"manifest"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Manifest.Name != "Preview dataset" || payload.Manifest.ApprovedBy != "owner" || payload.Manifest.PreviewChecksum == "" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAdminAuditsEndpointReturnsItems(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.Audit("import_source_create", "seed audit"); err != nil {
		t.Fatal(err)
	}
	if err := a.state.Audit("import_source_toggle", "seed audit 2"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/audits?limit=1&offset=1", nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Items []struct {
			Action  string `json:"action"`
			Details string `json:"details"`
		} `json:"items"`
		Limit      int  `json:"limit"`
		Offset     int  `json:"offset"`
		HasMore    bool `json:"hasMore"`
		NextLimit  int  `json:"nextLimit"`
		NextOffset int  `json:"nextOffset"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Limit != 1 || payload.Offset != 1 || payload.HasMore || payload.NextLimit != 1 || payload.NextOffset != 1 || len(payload.Items) != 1 || payload.Items[0].Action != "import_source_create" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAdminStatusEndpointIncludesSections(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	seedState(t, statePath)
	if err := a.state.Audit("import_source_create", "seed audit"); err != nil {
		t.Fatal(err)
	}

	cookie := loginAsAdmin(t, a)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/status", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Settings                 map[string]any   `json:"settings"`
		AdminSessionEpoch        int64            `json:"adminSessionEpoch"`
		LastLoginAt              any              `json:"lastLoginAt"`
		LastLogoutAt             any              `json:"lastLogoutAt"`
		AuditCount               int              `json:"auditCount"`
		CatalogHealthy           bool             `json:"catalogHealthy"`
		StateHealthy             bool             `json:"stateHealthy"`
		ImportSources            []map[string]any `json:"importSources"`
		ImportRuns               []map[string]any `json:"importRuns"`
		ImportManifests          []map[string]any `json:"importManifests"`
		ImportManifestCount      int              `json:"importManifestCount"`
		ImportManifestTotalCount int              `json:"importManifestTotalCount"`
		ImportManifestTotalBytes int64            `json:"importManifestTotalBytes"`
		Audits                   []map[string]any `json:"audits"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Settings == nil || payload.ImportSources == nil || payload.ImportRuns == nil || payload.ImportManifests == nil || payload.Audits == nil || !payload.CatalogHealthy || !payload.StateHealthy || payload.ImportManifestCount < 0 || payload.ImportManifestTotalCount < 0 || payload.ImportManifestTotalBytes < 0 || payload.AdminSessionEpoch != 0 || payload.LastLoginAt == nil || payload.LastLogoutAt != nil {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	if payload.AuditCount != len(payload.Audits) {
		t.Fatalf("expected auditCount to match returned audits, got %#v", payload)
	}

	if _, err := a.state.BumpAdminSessionEpoch(); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected old cookie to be revoked after bump, got %d body=%s", rec.Code, rec.Body.String())
	}
	bumpedCookie, err := a.signSession("admin", 1)
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/admin/status", nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: bumpedCookie})
	rec = httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code after bump with refreshed cookie: %d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.AdminSessionEpoch != 1 {
		t.Fatalf("expected bumped epoch in status payload, got %#v", payload)
	}
	if payload.LastLoginAt == nil {
		t.Fatalf("expected lastLoginAt in status payload after bump, got %#v", payload)
	}
	if payload.LastLogoutAt != nil {
		t.Fatalf("expected lastLogoutAt to remain nil before logout, got %#v", payload)
	}
	if payload.AuditCount != len(payload.Audits) {
		t.Fatalf("expected auditCount to match returned audits after bump, got %#v", payload)
	}

	adminPageReq := httptest.NewRequest(http.MethodGet, "/admin", nil)
	adminPageReq.AddCookie(&http.Cookie{Name: "admin_session", Value: bumpedCookie})
	adminPageRec := httptest.NewRecorder()
	a.Router().ServeHTTP(adminPageRec, adminPageReq)
	if adminPageRec.Code != http.StatusOK {
		t.Fatalf("unexpected admin page code after bump: %d body=%s", adminPageRec.Code, adminPageRec.Body.String())
	}
	adminPage := adminPageRec.Body.String()
	if !strings.Contains(adminPage, "Last login audit") || !strings.Contains(adminPage, "Last logout audit") || !strings.Contains(adminPage, "Admin session epoch") {
		t.Fatalf("expected admin page to include live status labels, got body=%s", adminPage)
	}
}

func TestAdminPagesDoNotDependOnWorkingDirectory(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})

	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	indexReq := httptest.NewRequest(http.MethodGet, "/", nil)
	indexRec := httptest.NewRecorder()
	a.Router().ServeHTTP(indexRec, indexReq)
	if indexRec.Code != http.StatusOK {
		t.Fatalf("unexpected index code from alternate cwd: %d body=%s", indexRec.Code, indexRec.Body.String())
	}

	torrentReq := httptest.NewRequest(http.MethodGet, "/torrent/10", nil)
	torrentRec := httptest.NewRecorder()
	a.Router().ServeHTTP(torrentRec, torrentReq)
	if torrentRec.Code != http.StatusOK {
		t.Fatalf("unexpected torrent code from alternate cwd: %d body=%s", torrentRec.Code, torrentRec.Body.String())
	}

	adminReq := httptest.NewRequest(http.MethodGet, "/admin", nil)
	adminRec := httptest.NewRecorder()
	a.Router().ServeHTTP(adminRec, adminReq)
	if adminRec.Code != http.StatusOK {
		t.Fatalf("unexpected admin code from alternate cwd: %d body=%s", adminRec.Code, adminRec.Body.String())
	}
	if !strings.Contains(adminRec.Body.String(), "Sign in") {
		t.Fatalf("expected login page to render from alternate cwd, got body=%s", adminRec.Body.String())
	}
}

func TestAdminPageReturnsServerErrorWhenTemplateMissing(t *testing.T) {
	sandboxTemplates(t)

	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	removeTemplate(t, "admin.html")

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(loginAsAdmin(t, a))
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected server error when %s is missing, got %d body=%s", "admin.html", rec.Code, rec.Body.String())
	}
}

func TestIndexPageReturnsServerErrorWhenBaseTemplateMissing(t *testing.T) {
	sandboxTemplates(t)

	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	removeTemplate(t, "base.html")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected server error for %s, got %d body=%s", "/", rec.Code, rec.Body.String())
	}
}

func TestIndexPageReturnsServerErrorWhenBaseTemplateInvalid(t *testing.T) {
	sandboxTemplates(t)

	mutateTemplate(t, "base.html", "{{")

	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected server error for %s, got %d body=%s", "/", rec.Code, rec.Body.String())
	}
}

func TestTorrentPageReturnsServerErrorWhenTemplateMissing(t *testing.T) {
	sandboxTemplates(t)

	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	removeTemplate(t, "torrent.html")

	req := httptest.NewRequest(http.MethodGet, "/torrent/10", nil)
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected server error for %s, got %d body=%s", "/torrent/10", rec.Code, rec.Body.String())
	}
}

func TestAdminLoginPageReturnsServerErrorWhenBaseTemplateMissing(t *testing.T) {
	sandboxTemplates(t)

	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	removeTemplate(t, "base.html")

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected server error for %s, got %d body=%s", "/admin", rec.Code, rec.Body.String())
	}
}

func TestAdminLoginPageReturnsServerErrorWhenLoginTemplateMissing(t *testing.T) {
	sandboxTemplates(t)

	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	removeTemplate(t, "admin_login.html")

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected server error for %s, got %d body=%s", "/admin", rec.Code, rec.Body.String())
	}
}

func removeTemplate(t *testing.T, name string) {
	t.Helper()
	templateFile := templatePath(name)
	backupFile := templateFile + ".bak"
	if err := os.Rename(templateFile, backupFile); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Rename(backupFile, templateFile)
	})
}

func mutateTemplate(t *testing.T, name, contents string) {
	t.Helper()
	templateFile := templatePath(name)
	original, err := os.ReadFile(templateFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(templateFile, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(templateFile, original, 0o600)
	})
}

func sandboxTemplates(t *testing.T) {
	t.Helper()

	dstDir := t.TempDir()

	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		dstPath := filepath.Join(dstDir, entry.Name())
		contents, err := templateFS.ReadFile("templates/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dstPath, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	prevTemplateDir := templateDir
	templateDir = dstDir
	t.Cleanup(func() {
		templateDir = prevTemplateDir
	})
}

func newAdminTestApp(t *testing.T, password string) (*App, string) {
	t.Helper()

	a, _, statePath := newTestApp(t, password)
	return a, statePath
}

func newTestApp(t *testing.T, password string) (*App, string, string) {
	t.Helper()

	t.Setenv("ADMIN_PASSWORD", password)
	t.Setenv("ADMIN_SESSION_SECRET", "01234567890123456789012345678901")

	catalogPath := filepath.Join(t.TempDir(), "catalog.sqlite")
	statePath := filepath.Join(t.TempDir(), "state.sqlite")
	seedCatalog(t, catalogPath)

	a, err := NewWithOptions(catalogPath, statePath, Options{DownloadClientPath: filepath.Join(t.TempDir(), "missing-aria2c")})
	if err != nil {
		t.Fatal(err)
	}
	return a, catalogPath, statePath
}

func TestEmbeddedTemplatesExist(t *testing.T) {
	for _, name := range []string{"base.html", "index.html", "torrent.html", "admin.html", "admin_login.html"} {
		if _, err := templateFS.ReadFile("templates/" + name); err != nil {
			t.Fatalf("expected embedded template %s to exist, got %v", name, err)
		}
	}
}

func TestEmbeddedTemplatesIgnoreWorkingDirectory(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})

	for _, name := range []string{"base.html", "index.html", "torrent.html", "admin.html", "admin_login.html"} {
		if _, err := templateFS.ReadFile("templates/" + name); err != nil {
			t.Fatalf("expected embedded template %s after chdir, got %v", name, err)
		}
	}
}

func TestReleaseVersionIsDisplayedAndReported(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", testAdminPassword)
	t.Setenv("ADMIN_SESSION_SECRET", testAdminSessionSecret)
	catalogPath := filepath.Join(t.TempDir(), "catalog.sqlite")
	statePath := filepath.Join(t.TempDir(), "state.sqlite")
	seedCatalog(t, catalogPath)

	a, err := NewWithOptions(catalogPath, statePath, Options{Version: "v9.9.9-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	pageReq := httptest.NewRequest(http.MethodGet, "/", nil)
	pageRec := httptest.NewRecorder()
	a.Router().ServeHTTP(pageRec, pageReq)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("unexpected page status: %d body=%s", pageRec.Code, pageRec.Body.String())
	}
	if !strings.Contains(pageRec.Body.String(), "Version</span><strong>v9.9.9-test") {
		t.Fatalf("expected release version in UI header, body=%s", pageRec.Body.String())
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	healthRec := httptest.NewRecorder()
	a.Router().ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("unexpected health status: %d body=%s", healthRec.Code, healthRec.Body.String())
	}
	var healthPayload struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(healthRec.Body.Bytes(), &healthPayload); err != nil {
		t.Fatal(err)
	}
	if healthPayload.Status != "ok" || healthPayload.Version != "v9.9.9-test" {
		t.Fatalf("unexpected health payload: %#v", healthPayload)
	}
}

func TestAdminAuditDetailEndpointReturnsItem(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.Audit("import_source_create", "seed audit"); err != nil {
		t.Fatal(err)
	}
	audits, err := a.state.AuditEntries(10)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/audits/detail?id="+strconv.FormatInt(audits[0].ID, 10), nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Audit struct {
			ID      int64  `json:"id"`
			Action  string `json:"action"`
			Details string `json:"details"`
		} `json:"audit"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Audit.ID != audits[0].ID || payload.Audit.Action != "import_source_create" || payload.Audit.Details != "seed audit" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAdminExportAuditsEndpointReturnsItems(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.Audit("import_source_create", "first"); err != nil {
		t.Fatal(err)
	}
	if err := a.state.Audit("import_source_toggle", "second"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/audits/export", nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Count int `json:"count"`
		Items []struct {
			Action string `json:"action"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 2 || len(payload.Items) != 2 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAdminExportAuditsEndpointDownloadsJson(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.Audit("import_source_create", "first"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/audits/export?download=1&limit=1&offset=0", nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="audits.json"` {
		t.Fatalf("unexpected disposition: %q", got)
	}
	var payload struct {
		Download bool `json:"download"`
		Count    int  `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Download || payload.Count != 1 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAdminDeleteAuditEntryEndpointDeletesItem(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.Audit("import_source_create", "first"); err != nil {
		t.Fatal(err)
	}
	entries, err := a.state.AuditEntries(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one audit entry to delete")
	}

	form := url.Values{"id": []string{strconv.FormatInt(entries[0].ID, 10)}}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/audits/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := a.state.AuditEntry(entries[0].ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected deleted audit entry to be missing, got %v", err)
	}
}

func TestAdminImportRunsListEndpointReturnsItems(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.CreateImportRun(1, "queued", "first", "checksum1", "ref1"); err != nil {
		t.Fatal(err)
	}
	if err := a.state.CreateImportRun(1, "running", "second", "checksum2", "ref2"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/import-runs/list?limit=1&offset=0", nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Items []struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"items"`
		Limit      int  `json:"limit"`
		Offset     int  `json:"offset"`
		HasMore    bool `json:"hasMore"`
		NextOffset int  `json:"nextOffset"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Limit != 1 || payload.Offset != 0 || !payload.HasMore || payload.NextOffset != 1 || len(payload.Items) != 1 || payload.Items[0].Message != "second" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAdminImportSourcesListEndpointReturnsItems(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.CreateImportSource("Alpha", "http", "https://example.com/a", true); err != nil {
		t.Fatal(err)
	}
	if err := a.state.CreateImportSource("Beta", "file", "/tmp/b.json", false); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/import-sources/list?limit=1&offset=0", nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
		Limit      int  `json:"limit"`
		Offset     int  `json:"offset"`
		HasMore    bool `json:"hasMore"`
		NextOffset int  `json:"nextOffset"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Limit != 1 || payload.Offset != 0 || !payload.HasMore || payload.NextOffset != 1 || len(payload.Items) != 1 || payload.Items[0].Name != "Beta" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAdminImportSourceDetailEndpointReturnsItem(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.CreateImportSource("Alpha", "http", "https://example.com/a", true); err != nil {
		t.Fatal(err)
	}
	if err := a.state.CreateImportSource("Beta", "file", "/tmp/b.json", false); err != nil {
		t.Fatal(err)
	}
	sources, err := a.state.ImportSources()
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/import-sources/detail?id="+strconv.FormatInt(sources[0].ID, 10), nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Source struct {
			ID      int64  `json:"id"`
			Name    string `json:"name"`
			Kind    string `json:"kind"`
			Enabled bool   `json:"enabled"`
		} `json:"source"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Source.ID != sources[0].ID || payload.Source.Name != "Beta" || payload.Source.Kind != "file" || payload.Source.Enabled {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAdminDeleteImportSourceEndpointDeletesItem(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.CreateImportSource("Alpha", "http", "https://example.com/a", true); err != nil {
		t.Fatal(err)
	}
	if err := a.state.CreateImportSource("Beta", "file", "/tmp/b.json", false); err != nil {
		t.Fatal(err)
	}
	sources, err := a.state.ImportSources()
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"id": []string{strconv.FormatInt(sources[0].ID, 10)}}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/import-sources/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Deleted bool  `json:"deleted"`
		ID      int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Deleted || payload.ID != sources[0].ID {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	if _, err := a.state.ImportSource(sources[0].ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected deleted source to be missing, got %v", err)
	}
}

func TestAdminExportImportSourcesEndpointReturnsItems(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.CreateImportSource("Alpha", "http", "https://example.com/a", true); err != nil {
		t.Fatal(err)
	}
	if err := a.state.CreateImportSource("Beta", "file", "/tmp/b.json", false); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/import-sources/export", nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Count int `json:"count"`
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 3 || len(payload.Items) != 3 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAdminExportImportSourcesEndpointDownloadsJson(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.CreateImportSource("Alpha", "http", "https://example.com/a", true); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/import-sources/export?download=1&limit=1&offset=0", nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="import-sources.json"` {
		t.Fatalf("unexpected disposition: %q", got)
	}
	var payload struct {
		Download bool `json:"download"`
		Count    int  `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Download || payload.Count != 1 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAdminImportSourcesListEndpointFiltersQuery(t *testing.T) {
	a, statePath := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	seedState(t, statePath)
	if err := a.state.CreateImportSource("Alpha", "http", "https://example.com/a", true); err != nil {
		t.Fatal(err)
	}
	if err := a.state.CreateImportSource("Beta", "file", "/tmp/b.json", false); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/import-sources/list?limit=10&offset=0&q=alp", nil)
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()

	a.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected code: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 1 || payload.Items[0].Name != "Alpha" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func seedCatalog(t *testing.T, path string) {
	t.Helper()
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
		`insert into torrents(id, category, status, name, numFiles, size, seeders, leechers, username, added, description, imdb, language, textLanguage, infoHash) values (10, 1, 'ok', 'Test Torrent', 3, 1048576, 7, 2, 'alice', 1710000000, 'hello world', 'tt1234567', 'English', 'English', '0123456789abcdef0123456789abcdef01234567')`,
		`insert into files(id, parentTorrentId, name, size) values (1, 10, 'file1.mkv', 1024), (2, 10, 'file2.srt', 2048)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAdminImportReferenceRecordsMagnetMetadataOnly(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"reference": []string{"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Ubuntu%2024.04&tr=udp%3A%2F%2Ftracker.example%3A80"}}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/import-references", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected import reference status: %d body=%s", rec.Code, rec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/admin/import-references/list", nil)
	listReq.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	listRec := httptest.NewRecorder()
	a.Router().ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("unexpected list status: %d body=%s", listRec.Code, listRec.Body.String())
	}
	var payload struct {
		Items []struct {
			Kind     string `json:"kind"`
			InfoHash string `json:"infoHash"`
			Name     string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 1 || payload.Items[0].Kind != "magnet" || payload.Items[0].InfoHash == "" || payload.Items[0].Name != "Ubuntu 24.04" {
		t.Fatalf("unexpected import reference payload: %#v", payload)
	}
}

func TestAdminImportReferenceAcceptsBrowserMultipartForm(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}
	magnet := "magnet:?xt=urn:btih:0D4CD209E72F28023692DFCA65345AA508F9BF7A&dn=The%20Pirate%20Bay%20%26amp%3B%20YTS%20-%20Full%20Database%20Backup%20-%202024-06"
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("magnet", magnet); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/import-references", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart magnet was rejected: %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "0D4CD209E72F28023692DFCA65345AA508F9BF7A") {
		t.Fatalf("expected parsed multipart magnet metadata, got %s", rec.Body.String())
	}
}

func TestCatalogPresetApprovalRunsRecoveryWorkflow(t *testing.T) {
	a, _, _ := newTestApp(t, testAdminPassword)
	defer a.Close()

	recoveredPath := filepath.Join(t.TempDir(), "preset-source.sqlite")
	seedCatalog(t, recoveredPath)
	db, err := sql.Open("sqlite", recoveredPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("update torrents set name = 'Preset recovered item'"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	scriptPath := filepath.Join(t.TempDir(), "aria2c")
	script := "#!/bin/sh\ndir=''\nfor arg in \"$@\"; do\n  case \"$arg\" in\n    --dir=*) dir=\"${arg#--dir=}\" ;;\n  esac\ndone\ncp \"$APP_TEST_PRESET_CATALOG\" \"$dir/preset-source.sqlite\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_TEST_PRESET_CATALOG", recoveredPath)
	a.aria2Path = scriptPath

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"preset_id": []string{"pirate-bay-yts-2024-06"},
		"approve":   []string{"1"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/import-references", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preset approval failed: %d body=%s", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(3 * time.Second)
	var item state.ImportReference
	for {
		item, err = a.state.ImportReference(1)
		if err != nil {
			t.Fatal(err)
		}
		if item.RecoveryStatus == "loaded" {
			break
		}
		if item.RecoveryStatus == "failed" || item.RecoveryError != "" {
			t.Fatalf("preset recovery failed: %#v", item)
		}
		if time.Now().After(deadline) {
			t.Fatalf("preset recovery did not finish: %#v", item)
		}
		time.Sleep(20 * time.Millisecond)
	}

	items, total, err := a.catalogSearchPage("Preset recovered item", "", "", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].Name != "Preset recovered item" {
		t.Fatalf("expected preset-loaded catalog to be searchable: total=%d items=%#v", total, items)
	}
	audits, err := a.state.AuditEntries(20)
	if err != nil {
		t.Fatal(err)
	}
	approved := false
	for _, audit := range audits {
		if audit.Action == "catalog_preset_approved" && strings.Contains(audit.Details, "pirate-bay-yts-2024-06") {
			approved = true
			break
		}
	}
	if !approved {
		t.Fatalf("expected preset approval audit, got %#v", audits)
	}
}

func TestAdminImportReferenceStartsConfiguredDownloadClient(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	argsPath := filepath.Join(t.TempDir(), "aria2-args.txt")
	scriptPath := filepath.Join(t.TempDir(), "aria2c")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$APP_TEST_ARIA2_ARGS\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_TEST_ARIA2_ARGS", argsPath)
	a.aria2Path = scriptPath

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}
	magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	form := url.Values{"magnet": []string{magnet}}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/import-references", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected import reference status: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Download struct {
			Started   bool   `json:"started"`
			Directory string `json:"directory"`
		} `json:"download"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Download.Started || payload.Download.Directory == "" {
		t.Fatalf("expected configured download client to start: %#v", payload)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(argsPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("configured download client was not invoked")
		}
		time.Sleep(10 * time.Millisecond)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), magnet) || !strings.Contains(string(args), "--dir="+payload.Download.Directory) {
		t.Fatalf("unexpected download client arguments: %q", string(args))
	}
}

func TestImportReferenceRecoversAndLoadsSQLiteCatalog(t *testing.T) {
	a, _, _ := newTestApp(t, testAdminPassword)
	defer a.Close()

	recoveredPath := filepath.Join(t.TempDir(), "recovered.sqlite")
	seedCatalog(t, recoveredPath)
	db, err := sql.Open("sqlite", recoveredPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("update torrents set name = 'Recovered search item'"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Recovered%20backup"
	item, err := a.state.CreateImportReference("magnet", magnet, "0123456789ABCDEF0123456789ABCDEF01234567", "Recovered backup", "")
	if err != nil {
		t.Fatal(err)
	}
	a.recoverReference(item.ID, item.Reference, item.Name, filepath.Dir(recoveredPath))

	name, _, _ := a.catalogInfo()
	if name != "Recovered backup" {
		t.Fatalf("expected recovered catalog to become active, got %q", name)
	}
	items, total, err := a.catalogSearchPage("Recovered search item", "", "", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].Name != "Recovered search item" {
		t.Fatalf("expected recovered catalog to be searchable, got total=%d items=%#v", total, items)
	}

	stored, err := a.state.ImportReference(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RecoveryStatus != "loaded" || stored.RecoveredPath != recoveredPath {
		t.Fatalf("unexpected persisted recovery status: %#v", stored)
	}
	sources, err := a.state.CatalogSources()
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Magnet != magnet || sources[0].CatalogPath != recoveredPath || !sources[0].Enabled {
		t.Fatalf("expected loaded magnet to become a customer source, got %#v", sources)
	}
}

func TestCatalogSourcesPublicEndpointReturnsEnabledMagnetsOnly(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	if err := a.state.CreateCatalogSource("Primary backup", "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", "/private/primary.sqlite", true); err != nil {
		t.Fatal(err)
	}
	if err := a.state.CreateCatalogSource("Hidden backup", "magnet:?xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01", "/private/hidden.sqlite", false); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/catalog-sources", nil)
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Items []struct {
			Name        string `json:"name"`
			Magnet      string `json:"magnet"`
			CatalogPath string `json:"catalogPath"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 1 || payload.Items[0].Name != "Primary backup" || payload.Items[0].Magnet == "" || payload.Items[0].CatalogPath != "" {
		t.Fatalf("unexpected public catalog source payload: %#v", payload)
	}
}

func TestAdminCatalogSourceLoadSwapsReadOnlyCatalog(t *testing.T) {
	a, configuredPath, statePath := newTestApp(t, testAdminPassword)
	defer a.Close()

	replacementPath := filepath.Join(t.TempDir(), "recovered.sqlite")
	seedCatalog(t, replacementPath)
	db, err := sql.Open("sqlite", replacementPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("update torrents set name = 'Recovered Catalog Item'"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.state.CreateCatalogSource("Recovered backup", "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", replacementPath, true); err != nil {
		t.Fatal(err)
	}
	sources, err := a.state.CatalogSources()
	if err != nil {
		t.Fatal(err)
	}
	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"source_id": []string{strconv.FormatInt(sources[0].ID, 10)}}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/catalog-sources/load", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected load status: %d body=%s", rec.Code, rec.Body.String())
	}
	var loadPayload struct {
		Loaded bool `json:"loaded"`
		Source struct {
			Name string `json:"name"`
		} `json:"source"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &loadPayload); err != nil {
		t.Fatal(err)
	}
	if !loadPayload.Loaded || loadPayload.Source.Name != "Recovered backup" {
		t.Fatalf("unexpected load payload: %#v", loadPayload)
	}

	searchReq := httptest.NewRequest(http.MethodGet, "/api/search?q=Recovered+Catalog", nil)
	searchRec := httptest.NewRecorder()
	a.Router().ServeHTTP(searchRec, searchReq)
	if searchRec.Code != http.StatusOK {
		t.Fatalf("unexpected search status: %d body=%s", searchRec.Code, searchRec.Body.String())
	}
	var searchPayload struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(searchRec.Body.Bytes(), &searchPayload); err != nil {
		t.Fatal(err)
	}
	if len(searchPayload.Items) != 1 || searchPayload.Items[0].Name != "Recovered Catalog Item" {
		t.Fatalf("expected search to use recovered catalog, got %#v", searchPayload)
	}

	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(configuredPath, statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restartedSearchReq := httptest.NewRequest(http.MethodGet, "/api/search?q=Recovered+Catalog", nil)
	restartedSearchRec := httptest.NewRecorder()
	restarted.Router().ServeHTTP(restartedSearchRec, restartedSearchReq)
	if restartedSearchRec.Code != http.StatusOK {
		t.Fatalf("unexpected restarted search status: %d body=%s", restartedSearchRec.Code, restartedSearchRec.Body.String())
	}
	var restartedSearchPayload struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(restartedSearchRec.Body.Bytes(), &restartedSearchPayload); err != nil {
		t.Fatal(err)
	}
	if len(restartedSearchPayload.Items) != 1 || restartedSearchPayload.Items[0].Name != "Recovered Catalog Item" {
		t.Fatalf("expected active catalog to persist across restart, got %#v", restartedSearchPayload)
	}
}

func TestAdminCatalogSourceLoadRejectsInvalidCatalogWithoutSwap(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	invalidPath := filepath.Join(t.TempDir(), "invalid.sqlite")
	if err := os.WriteFile(invalidPath, []byte("not a sqlite catalog"), 0o600); err != nil {
		t.Fatal(err)
	}
	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"catalog_path": []string{invalidPath}}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/catalog-sources/load", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid catalog to be rejected, got %d body=%s", rec.Code, rec.Body.String())
	}

	searchReq := httptest.NewRequest(http.MethodGet, "/api/search?q=Test+Torrent", nil)
	searchRec := httptest.NewRecorder()
	a.Router().ServeHTTP(searchRec, searchReq)
	if searchRec.Code != http.StatusOK {
		t.Fatalf("unexpected search status: %d body=%s", searchRec.Code, searchRec.Body.String())
	}
	var searchPayload struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(searchRec.Body.Bytes(), &searchPayload); err != nil {
		t.Fatal(err)
	}
	if len(searchPayload.Items) != 1 || searchPayload.Items[0].Name != "Test Torrent" {
		t.Fatalf("invalid load should leave the current catalog active, got %#v", searchPayload)
	}
}

func TestAdminCatalogSourceCreateLoadsLocalCatalogAndPublishesRevision(t *testing.T) {
	a, _, _ := newTestApp(t, testAdminPassword)
	defer a.Close()

	replacementPath := filepath.Join(t.TempDir(), "recovered-on-create.sqlite")
	seedCatalog(t, replacementPath)
	db, err := sql.Open("sqlite", replacementPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("update torrents set name = 'Recovered on Create'"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"name":         []string{"Recovered on create"},
		"magnet":       []string{"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"},
		"catalog_path": []string{replacementPath},
		"enabled":      []string{"1"},
		"load_now":     []string{"1"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/catalog-sources", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("unexpected create status: %d body=%s", rec.Code, rec.Body.String())
	}

	searchReq := httptest.NewRequest(http.MethodGet, "/api/search?q=Recovered+on+Create", nil)
	searchRec := httptest.NewRecorder()
	a.Router().ServeHTTP(searchRec, searchReq)
	if searchRec.Code != http.StatusOK {
		t.Fatalf("unexpected search status: %d body=%s", searchRec.Code, searchRec.Body.String())
	}
	var searchPayload struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(searchRec.Body.Bytes(), &searchPayload); err != nil {
		t.Fatal(err)
	}
	if len(searchPayload.Items) != 1 || searchPayload.Items[0].Name != "Recovered on Create" {
		t.Fatalf("expected create flow to activate recovered catalog, got %#v", searchPayload)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/catalog-status", nil)
	statusRec := httptest.NewRecorder()
	a.Router().ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("unexpected catalog status: %d body=%s", statusRec.Code, statusRec.Body.String())
	}
	var statusPayload struct {
		Name     string `json:"name"`
		Revision uint64 `json:"revision"`
	}
	if err := json.Unmarshal(statusRec.Body.Bytes(), &statusPayload); err != nil {
		t.Fatal(err)
	}
	if statusPayload.Name != "Recovered on create" || statusPayload.Revision < 2 {
		t.Fatalf("unexpected catalog status payload: %#v", statusPayload)
	}
}

func TestAdminCatalogSourceUploadValidatesRegistersAndLoads(t *testing.T) {
	a, _, statePath := newTestApp(t, testAdminPassword)
	defer a.Close()

	sourcePath := filepath.Join(t.TempDir(), "uploaded-source.sqlite")
	seedCatalog(t, sourcePath)
	db, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("update torrents set name = 'Uploaded Recovery Catalog'"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{
		"name":     "Uploaded recovery",
		"magnet":   "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
		"enabled":  "1",
		"load_now": "1",
	} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("catalog_file", "recovered.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	cookieVal, err := a.signSession("admin")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/catalog-sources/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "admin_session", Value: cookieVal})
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("unexpected upload status: %d body=%s", rec.Code, rec.Body.String())
	}

	sources, err := a.state.CatalogSources()
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Name != "Uploaded recovery" || sources[0].Magnet == "" {
		t.Fatalf("unexpected uploaded source: %#v", sources)
	}
	if sources[0].CatalogPath == "" || filepath.Dir(sources[0].CatalogPath) != filepath.Join(filepath.Dir(statePath), "recovery-catalogs") {
		t.Fatalf("expected uploaded catalog under recovery directory, got %#v", sources[0])
	}
	if _, err := os.Stat(sources[0].CatalogPath); err != nil {
		t.Fatalf("uploaded catalog was not installed: %v", err)
	}

	searchReq := httptest.NewRequest(http.MethodGet, "/api/search?q=Uploaded+Recovery", nil)
	searchRec := httptest.NewRecorder()
	a.Router().ServeHTTP(searchRec, searchReq)
	if searchRec.Code != http.StatusOK {
		t.Fatalf("unexpected search status: %d body=%s", searchRec.Code, searchRec.Body.String())
	}
	var searchPayload struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(searchRec.Body.Bytes(), &searchPayload); err != nil {
		t.Fatal(err)
	}
	if len(searchPayload.Items) != 1 || searchPayload.Items[0].Name != "Uploaded Recovery Catalog" {
		t.Fatalf("expected uploaded catalog to be active, got %#v", searchPayload)
	}
}

func TestAdminLoginRejectsCrossOriginRequests(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	form := url.Values{"password": []string{testAdminPassword}}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://attacker.example")
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected cross-origin login to be rejected, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSearchEndpointReturnsPaginationMetadata(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	requests := []struct {
		path       string
		offset     int
		items      int
		nextOffset int
	}{
		{path: "/api/search?limit=1&offset=0&sort=newest", offset: 0, items: 1, nextOffset: 0},
		{path: "/api/search?limit=1&offset=1&sort=newest", offset: 1, items: 0, nextOffset: 1},
		{path: "/api/search?limit=1&category=1", offset: 0, items: 1, nextOffset: 0},
	}
	for _, test := range requests {
		rec := httptest.NewRecorder()
		a.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, test.path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("unexpected search status for %s: %d body=%s", test.path, rec.Code, rec.Body.String())
		}
		var payload struct {
			Items      []map[string]any `json:"items"`
			Total      int64            `json:"total"`
			Offset     int              `json:"offset"`
			HasMore    bool             `json:"hasMore"`
			NextOffset int              `json:"nextOffset"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Total != 1 || payload.Offset != test.offset || len(payload.Items) != test.items || payload.HasMore || payload.NextOffset != test.nextOffset {
			t.Fatalf("unexpected pagination metadata for %s: %#v", test.path, payload)
		}
	}
}

func TestSecurityHeadersIncludeHSTSForTLSRequests(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.TLS = &tls.ConnectionState{}
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if got := rec.Header().Get("Strict-Transport-Security"); !strings.Contains(got, "max-age=31536000") {
		t.Fatalf("expected HSTS for TLS request, got %q", got)
	}
}

func TestSameOriginRejectsCrossSchemeRequests(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", nil)
	req.Host = "catalog.example"
	req.Header.Set("Origin", "http://catalog.example")
	req.TLS = &tls.ConnectionState{}
	if sameOrigin(req) {
		t.Fatal("expected an HTTP origin on an HTTPS request to be rejected")
	}
	req.TLS = nil
	req.Header.Set("Origin", "https://catalog.example")
	if sameOrigin(req) {
		t.Fatal("expected an HTTPS origin on an HTTP request to be rejected")
	}
	req.Header.Set("Origin", "http://catalog.example")
	if !sameOrigin(req) {
		t.Fatal("expected matching HTTP origin to be accepted")
	}
}

func TestSubtleConstantTimeAcceptsEqualValuesOnly(t *testing.T) {
	if !subtleConstantTime([]byte("secret"), []byte("secret")) {
		t.Fatal("expected equal values to compare true")
	}
	if subtleConstantTime([]byte("secret"), []byte("different length")) || subtleConstantTime([]byte("secret"), []byte("wrong")) {
		t.Fatal("expected unequal values to compare false")
	}
}

func TestTorrentPageRendersValidatedMagnetLink(t *testing.T) {
	a, _ := newAdminTestApp(t, testAdminPassword)
	defer a.Close()

	req := httptest.NewRequest(http.MethodGet, "/torrent/10", nil)
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected torrent page status: %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&amp;dn=Test") {
		t.Fatalf("expected a validated magnet link in torrent page, got body=%s", body)
	}
	if !strings.Contains(body, "Open magnet") || !strings.Contains(body, "Copy magnet") {
		t.Fatalf("expected magnet actions in torrent page, got body=%s", body)
	}
}

func TestMagnetLinkRejectsInvalidInfoHashes(t *testing.T) {
	if got := magnetLink("not-a-hash", "Ubuntu"); got != "" {
		t.Fatalf("expected invalid info hash to be rejected, got %q", got)
	}
	if got := magnetLink("A", "Ubuntu"); got != "" {
		t.Fatalf("expected short info hash to be rejected, got %q", got)
	}
}

func seedState(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`create table if not exists import_sources (id integer primary key autoincrement, name text not null, kind text not null, location text not null, enabled integer not null default 1, created_at integer not null)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into import_sources(name, kind, location, enabled, created_at) values('Main feed', 'http', 'https://example.com', 1, 1710000000)`); err != nil {
		t.Fatal(err)
	}
}
