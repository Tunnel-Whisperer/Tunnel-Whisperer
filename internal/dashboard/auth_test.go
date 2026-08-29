package dashboard

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

func newAuthTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	tok, err := EnsureDashboardToken()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{sessions: newSessionStore()}
	return s, tok
}

func TestAuthMiddlewareBlocksAndAllows(t *testing.T) {
	s, tok := newAuthTestServer(t)
	guarded := s.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("secret"))
	}))

	// Unauthenticated API request → 401.
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()
	guarded.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated API: want 401, got %d", rec.Code)
	}

	// Unauthenticated browser navigation → redirect to /login.
	req = httptest.NewRequest(http.MethodGet, "/servers", nil)
	req.Header.Set("Accept", "text/html")
	rec = httptest.NewRecorder()
	guarded.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") == "" {
		t.Fatalf("unauthenticated browser: want 303 redirect, got %d (loc %q)", rec.Code, rec.Header().Get("Location"))
	}

	// Correct bearer token → allowed.
	req = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()
	guarded.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid bearer: want 200, got %d", rec.Code)
	}

	// Wrong bearer token → 401.
	req = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Authorization", "Bearer not-the-token")
	rec = httptest.NewRecorder()
	guarded.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong bearer: want 401, got %d", rec.Code)
	}

	// Static assets are exempt.
	req = httptest.NewRequest(http.MethodGet, "/static/css/style.css", nil)
	rec = httptest.NewRecorder()
	guarded.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("static asset should be exempt: got %d", rec.Code)
	}
}

func TestSessionCookieAuth(t *testing.T) {
	s, tok := newAuthTestServer(t)
	id, err := s.sessions.create(tok)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: id})
	if !s.isAuthenticated(req) {
		t.Fatal("valid session cookie should authenticate")
	}
	s.sessions.delete(id)
	if s.isAuthenticated(req) {
		t.Fatal("deleted session must not authenticate")
	}
}

func TestTokenRotateKillsSession(t *testing.T) {
	s, tok := newAuthTestServer(t)
	id, err := s.sessions.create(tok)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: id})
	if !s.isAuthenticated(req) {
		t.Fatal("session should authenticate before rotate")
	}
	if _, err := RotateDashboardToken(); err != nil {
		t.Fatal(err)
	}
	if s.isAuthenticated(req) {
		t.Fatal("session bound to the old token must die when the token is rotated")
	}
}

func TestSafeNext(t *testing.T) {
	cases := map[string]string{
		"/servers":            "/servers",
		"/config?tab=1":       "/config?tab=1",
		"":                    "/",
		"//evil.example":      "/",
		"https://evil.example": "/",
		"/\\evil":             "/",
	}
	for in, want := range cases {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTokenRotateInvalidatesOld(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	old, err := EnsureDashboardToken()
	if err != nil {
		t.Fatal(err)
	}
	if !tokenValid(old) {
		t.Fatal("freshly created token should validate")
	}
	rotated, err := RotateDashboardToken()
	if err != nil {
		t.Fatal(err)
	}
	if rotated == old {
		t.Fatal("rotate must produce a different token")
	}
	if tokenValid(old) {
		t.Fatal("old token must stop validating after rotate")
	}
	if !tokenValid(rotated) {
		t.Fatal("rotated token should validate")
	}
}
