package dashboard

import (
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/tunnelwhisperer/tw/internal/ops"
)

// Server serves the web dashboard.
type Server struct {
	ops   *ops.Ops
	addr  string
	mux   *http.ServeMux
	pages map[string]*template.Template
	sse   *sseHub
	logs  *logBuffer
}

// NewServer creates a dashboard server.
func NewServer(addr string, o *ops.Ops) *Server {
	s := &Server{
		ops:   o,
		addr:  addr,
		mux:   http.NewServeMux(),
		pages: make(map[string]*template.Template),
		sse:   newSSEHub(),
		logs:  newLogBuffer(500),
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
}

func (s *Server) routes() {
	// Static files.
	staticSub, _ := fs.Sub(staticFS, "static")
	s.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	// Pages.
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/relay", s.handleRelay)
	s.mux.HandleFunc("/relay/wizard", s.handleRelayWizard)
	s.mux.HandleFunc("/users", s.handleUsers)
	s.mux.HandleFunc("/users/new", s.handleUserNew)
	s.mux.HandleFunc("/users/", s.handleUserDetail) // /users/{name}
	s.mux.HandleFunc("/config", s.handleConfig)

	// REST API — read-only.
	s.mux.HandleFunc("/api/status", s.apiStatus)
	s.mux.HandleFunc("/api/config", s.apiConfig)
	s.mux.HandleFunc("/api/providers", s.apiProviders)
	s.mux.HandleFunc("/api/relay", s.apiRelay)

	// REST API — write.
	s.mux.HandleFunc("/api/mode", s.apiSetMode)
	s.mux.HandleFunc("/api/proxy", s.apiSetProxy)
	s.mux.HandleFunc("/api/log-level", s.apiSetLogLevel)
	s.mux.HandleFunc("/api/relay/test-creds", s.apiTestCreds)
	s.mux.HandleFunc("/api/relay/provision", s.apiProvisionRelay)
	s.mux.HandleFunc("/api/relay/destroy", s.apiDestroyRelay)
	s.mux.HandleFunc("/api/relay/test", s.apiTestRelay)
	s.mux.HandleFunc("/api/relay/ssh", s.apiRelaySSH)
	s.mux.HandleFunc("/api/relay/generate-script", s.apiGenerateScript)
	s.mux.HandleFunc("/api/relay/save-manual", s.apiSaveManualRelay)
	s.mux.HandleFunc("/api/server/start", s.apiServerStart)
	s.mux.HandleFunc("/api/server/stop", s.apiServerStop)
	s.mux.HandleFunc("/api/server/restart", s.apiServerRestart)
	s.mux.HandleFunc("/api/client/start", s.apiClientStart)
	s.mux.HandleFunc("/api/client/stop", s.apiClientStop)
	s.mux.HandleFunc("/api/client/reconnect", s.apiClientReconnect)
	s.mux.HandleFunc("/api/client/upload", s.apiClientUpload)
	s.mux.HandleFunc("/api/users", s.apiUsers)
	s.mux.HandleFunc("/api/users/apply", s.apiApplyUsers)
	s.mux.HandleFunc("/api/users/unregister", s.apiUnregisterUsers)
	s.mux.HandleFunc("/api/users/online", s.apiOnlineUsers)
	s.mux.HandleFunc("/api/users/", s.apiUserAction) // delete, download

	// SSE.
	s.mux.HandleFunc("/api/events/", s.apiEvents)
	s.mux.HandleFunc("/api/logs", s.apiLogs)
}

// Run starts the HTTP server (blocking).
func (s *Server) Run() error {
	slog.Info("dashboard listening", "addr", s.addr)
	return http.ListenAndServe(s.addr, s.mux)
}

// pageData is the common data passed to all page templates.
type pageData struct {
	Title  string
	Active string // nav highlight
	Mode   string // "server", "client", or ""
}
