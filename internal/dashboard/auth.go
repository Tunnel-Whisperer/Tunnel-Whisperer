package dashboard

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tunnelwhisperer/tw/internal/config"
)

const (
	sessionCookie = "tw_session"
	sessionTTL    = 12 * time.Hour
	tokenBytes    = 32
)

// EnsureDashboardToken returns the dashboard login token, generating and
// persisting a new one (0600) if none exists yet. Both the CLI (`tw dashboard
// token`) and the running dashboard call this, so they always agree.
func EnsureDashboardToken() (string, error) {
	if tok, err := readDashboardToken(); err == nil && tok != "" {
		return tok, nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return generateDashboardToken()
}

// RotateDashboardToken generates a fresh token, replacing any existing one.
// Every existing browser session is bound to the token in effect when it was
// created, so a rotation immediately invalidates all of them (see
// sessionStore.valid) — a running dashboard needs no notification.
func RotateDashboardToken() (string, error) {
	return generateDashboardToken()
}

func generateDashboardToken() (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating dashboard token: %w", err)
	}
	tok := hex.EncodeToString(raw)
	if err := os.MkdirAll(config.Dir(), 0700); err != nil {
		return "", fmt.Errorf("creating config dir: %w", err)
	}
	if err := os.WriteFile(config.DashboardTokenPath(), []byte(tok+"\n"), 0600); err != nil {
		return "", fmt.Errorf("writing dashboard token: %w", err)
	}
	return tok, nil
}

func readDashboardToken() (string, error) {
	data, err := os.ReadFile(config.DashboardTokenPath())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// tokenValid compares a presented token against the on-disk token in constant
// time. It reads the file per call so a rotated token takes effect immediately.
func tokenValid(presented string) bool {
	want, err := readDashboardToken()
	if err != nil || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(presented)), []byte(want)) == 1
}

// session is one browser login: when it expires, and the token it was
// authorized against (so rotating the token drops the session).
type session struct {
	expiry time.Time
	token  string
}

// sessionStore holds opaque browser session IDs. Sessions live only in memory,
// so a dashboard restart invalidates them all.
type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]session
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: make(map[string]session)}
}

// create issues a session bound to token (the dashboard token in effect at
// login). A later rotation changes the on-disk token, so valid() will reject
// this session.
func (s *sessionStore) create(token string) (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw)
	s.mu.Lock()
	s.sessions[id] = session{expiry: time.Now().Add(sessionTTL), token: token}
	s.mu.Unlock()
	return id, nil
}

// valid reports whether id is a live session for currentToken. An expired
// session, or one bound to a superseded token, is dropped and rejected.
func (s *sessionStore) valid(id, currentToken string) bool {
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return false
	}
	if time.Now().After(sess.expiry) || sess.token != currentToken {
		delete(s.sessions, id)
		return false
	}
	return true
}

func (s *sessionStore) delete(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// authExempt reports whether a path is reachable without authentication.
func authExempt(path string) bool {
	return path == "/login" ||
		path == "/logout" ||
		strings.HasPrefix(path, "/static/")
}

// isAuthenticated accepts either a bearer token (CLI/programmatic callers) or a
// valid browser session cookie.
func (s *Server) isAuthenticated(r *http.Request) bool {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		if tokenValid(strings.TrimPrefix(h, "Bearer ")) {
			return true
		}
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		if cur, err := readDashboardToken(); err == nil && cur != "" && s.sessions.valid(c.Value, cur) {
			return true
		}
	}
	return false
}

// authMiddleware gates every non-exempt route. Unauthenticated browser
// navigations are redirected to the login page; everything else (API, XHR,
// SSE, WebSocket) gets a 401.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authExempt(r.URL.Path) || s.isAuthenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		if wantsHTML(r) {
			http.Redirect(w, r, "/login?next="+safeNextEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		http.Error(w, "unauthorized: present a dashboard token (Authorization: Bearer …) or log in", http.StatusUnauthorized)
	})
}

func wantsHTML(r *http.Request) bool {
	return r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html")
}
