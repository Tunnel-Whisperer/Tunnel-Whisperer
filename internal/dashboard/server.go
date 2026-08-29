// Package dashboard serves the server-rendered HTML web UI. It pushes real-time
// updates over Server-Sent Events, exposes a relay SSH terminal over WebSocket,
// and is a thin front-end that calls into internal/ops.
package dashboard

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tunnelwhisperer/tw/internal/ops"
)

// Server serves the web dashboard.
type Server struct {
	ops       *ops.Ops
	addr      string
	mux       *http.ServeMux
	httpSrv   *http.Server
	pages     map[string]*template.Template
	loginTmpl *template.Template
	sse       *sseHub
	logs      *logBuffer
	sessions  *sessionStore
}

// NewServer creates a dashboard server.
func NewServer(addr string, o *ops.Ops) *Server {
	s := &Server{
		ops:      o,
		addr:     addr,
		mux:      http.NewServeMux(),
		pages:    make(map[string]*template.Template),
		sse:      newSSEHub(),
		logs:     newLogBuffer(500),
		sessions: newSessionStore(),
	}
	// Ensure a login token exists so `tw dashboard token` and the running
	// dashboard agree from the first request.
	if _, err := EnsureDashboardToken(); err != nil {
		slog.Error("could not initialize dashboard login token", "error", err)
	}
	s.installLogHandler()
	s.parseTemplates()
	s.routes()
	return s
}

// installLogHandler wraps the current slog handler with a tee that also
// writes to the dashboard's log buffer for real-time console streaming.
func (s *Server) installLogHandler() {
	current := slog.Default().Handler()
	if current == nil {
		current = slog.NewTextHandler(os.Stderr, nil)
	}
	slog.SetDefault(slog.New(newTeeHandler(current, s.logs)))
}

func (s *Server) parseTemplates() {
	// Parse the base templates (layout + partials) once.
	base := template.Must(template.New("").ParseFS(templateFS,
		"templates/layout.html",
		"templates/partials/*.html",
	))

	// For each page, clone the base and parse just that page file.
	pages, err := fs.Glob(templateFS, "templates/pages/*.html")
	if err != nil {
		panic(fmt.Sprintf("dashboard: globbing page templates: %v", err))
	}

	for _, page := range pages {
		name := strings.TrimSuffix(filepath.Base(page), ".html")
		clone, err := base.Clone()
		if err != nil {
			panic(fmt.Sprintf("dashboard: cloning base template for %s: %v", name, err))
		}
		tmpl := template.Must(clone.ParseFS(templateFS, page))
		s.pages[name] = tmpl
	}

	// The login page stands alone (no nav/layout — it is shown to
	// unauthenticated visitors).
	s.loginTmpl = template.Must(template.ParseFS(templateFS, "templates/login.html"))
}

func (s *Server) routes() {
	// Static files.
	staticSub, _ := fs.Sub(staticFS, "static")
	s.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	// Authentication (unauthenticated routes — see authExempt).
	s.mux.HandleFunc("/login", s.handleLogin)
	s.mux.HandleFunc("/logout", s.handleLogout)

	// Pages.
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/relay", s.handleRelay)
	s.mux.HandleFunc("/relay/wizard", s.handleRelayWizard)
	s.mux.HandleFunc("/servers", s.handleServers)
	s.mux.HandleFunc("/users", s.handleUsers)
	s.mux.HandleFunc("/users/", s.handleUserDetail) // /users/{name}
	s.mux.HandleFunc("/apps", s.handleApps)
	s.mux.HandleFunc("/apps/new", s.handleAppNew)
	s.mux.HandleFunc("/apps/edit/", s.handleAppEdit)
	s.mux.HandleFunc("/bandwidth", s.handleBandwidth)
	s.mux.HandleFunc("/config", s.handleConfig)

	// REST API — read-only.
	s.mux.HandleFunc("/api/status", s.apiStatus)
	s.mux.HandleFunc("/api/config", s.apiConfig)
	s.mux.HandleFunc("/api/providers", s.apiProviders)
	s.mux.HandleFunc("/api/relay", s.apiRelay)
	s.mux.HandleFunc("/api/stats", s.apiStats)
	s.mux.HandleFunc("/metrics", s.apiMetrics)

	// REST API — write.
	s.mux.HandleFunc("/api/mode", s.apiSetMode)
	s.mux.HandleFunc("/api/proxy", s.apiSetProxy)
	s.mux.HandleFunc("/api/log-level", s.apiSetLogLevel)
	s.mux.HandleFunc("/api/settings/server", s.apiSetServerSettings)
	s.mux.HandleFunc("/api/settings/xray", s.apiSetXraySettings)
	s.mux.HandleFunc("/api/settings/client", s.apiSetClientSettings)
	s.mux.HandleFunc("/api/settings/analytics", s.apiSetAnalyticsSettings)
	s.mux.HandleFunc("/api/relay/test-creds", s.apiTestCreds)
	s.mux.HandleFunc("/api/relay/provision", s.apiProvisionRelay)
	s.mux.HandleFunc("/api/relay/destroy", s.apiDestroyRelay)
	s.mux.HandleFunc("/api/relay/test", s.apiTestRelay)
	s.mux.HandleFunc("/api/relay/ssh", s.apiRelaySSH)
	s.mux.HandleFunc("/api/relay/generate-script", s.apiGenerateScript)
	s.mux.HandleFunc("/api/relay/save-manual", s.apiSaveManualRelay)
	s.mux.HandleFunc("/api/relay/close-ssh", s.apiCloseRelaySSH)
	s.mux.HandleFunc("/api/server/start", s.apiServerStart)
	s.mux.HandleFunc("/api/server/stop", s.apiServerStop)
	s.mux.HandleFunc("/api/server/restart", s.apiServerRestart)
	s.mux.HandleFunc("/api/client/start", s.apiClientStart)
	s.mux.HandleFunc("/api/client/stop", s.apiClientStop)
	s.mux.HandleFunc("/api/client/reconnect", s.apiClientReconnect)
	s.mux.HandleFunc("/api/client/port-override", s.apiClientPortOverride)
	s.mux.HandleFunc("/api/client/upload", s.apiClientUpload)
	s.mux.HandleFunc("/api/apps", s.apiApps)
	s.mux.HandleFunc("/api/apps/", s.apiAppAction)
	s.mux.HandleFunc("/api/config/contexts", s.apiListContexts)
	s.mux.HandleFunc("/api/config/use-context", s.apiUseContext)
	s.mux.HandleFunc("/api/servers", s.apiServers)
	s.mux.HandleFunc("/api/servers/unenroll", s.apiUnenrollServer)
	s.mux.HandleFunc("/api/users", s.apiUsers)
	s.mux.HandleFunc("/api/users/apply", s.apiApplyUsers)
	s.mux.HandleFunc("/api/users/unregister", s.apiUnregisterUsers)
	s.mux.HandleFunc("/api/users/online", s.apiOnlineUsers)
	s.mux.HandleFunc("/api/users/", s.apiUserAction) // delete, single-session

	// SSE.
	s.mux.HandleFunc("/api/events/", s.apiEvents)
	s.mux.HandleFunc("/api/logs", s.apiLogs)
}

// Run starts the HTTP server (blocking).
func (s *Server) Run() error {
	// The dashboard serves its login token and session cookie over cleartext
	// HTTP. Binding off-loopback exposes them to any passive observer on the
	// network, so refuse it unless the operator has explicitly opted in — and
	// warn loudly even then (finding SP-8).
	if host, _, err := net.SplitHostPort(s.addr); err == nil && !isLoopbackHost(host) {
		if !s.ops.Config().Server.DashboardAllowLAN {
			return fmt.Errorf("refusing to bind the dashboard to a non-loopback address (%s) over cleartext HTTP: "+
				"the login token and session cookie would be exposed on the network. "+
				"Keep dashboard_listen on 127.0.0.1 and use an SSH port-forward, or front it with a TLS "+
				"terminator and set server.dashboard_allow_lan: true to acknowledge the risk", s.addr)
		}
		slog.Warn("SECURITY: dashboard is bound off-loopback over CLEARTEXT HTTP; its bearer token and session "+
			"cookie are exposed to the network — put a TLS terminator in front and restrict access to a trusted network",
			"addr", s.addr)
	}

	s.httpSrv = &http.Server{
		Addr:    s.addr,
		Handler: s.securityHeaders(s.authMiddleware(s.mux)),
		// Bound how long a client may take to send its request headers, so a
		// slow-loris cannot pin connections open indefinitely (finding SP-19).
		// ReadTimeout/WriteTimeout are deliberately left unset — the SSE log
		// stream and the xterm.js WebSocket are long-lived.
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	slog.Info("dashboard listening", "addr", s.addr)
	err := s.httpSrv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// securityHeaders sets defensive response headers on every dashboard response
// (finding SP-15): block framing/clickjacking, MIME sniffing, and cross-origin
// resource loads. The CSP keeps 'unsafe-inline' for the app's own inline
// scripts/styles but forbids any external origin, so an injected string cannot
// exfiltrate to or load from another host, and `frame-ancestors 'none'`
// prevents embedding.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; " +
		"script-src 'self' 'unsafe-inline'; " +
		"style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data:; " +
		"connect-src 'self'; " +
		"font-src 'self'; " +
		"base-uri 'self'; " +
		"form-action 'self'; " +
		"frame-ancestors 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// isLoopbackHost reports whether host is a loopback bind. An empty host means
// "all interfaces" and is therefore NOT loopback.
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Stop gracefully shuts down the HTTP server.
func (s *Server) Stop() error {
	if s.httpSrv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.httpSrv.Shutdown(ctx)
}

// pageData is the common data passed to all page templates.
type pageData struct {
	Title   string
	Active  string // nav highlight
	Mode    string // "server", "client", "relay", or ""
	Context string // active context name ("" if none stored)
}

// newPageData fills the fields every page shares. CurrentContext is a local
// index read; an error just leaves the nav badge empty.
func (s *Server) newPageData(title, active string) pageData {
	ctx, _ := s.ops.CurrentContext()
	return pageData{Title: title, Active: active, Mode: s.ops.Mode(), Context: ctx}
}
