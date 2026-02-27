package dashboard

import (
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
	"gopkg.in/yaml.v3"
)

func cappedUsers(users []ops.UserInfo, max int) []ops.UserInfo {
	if len(users) <= max {
		return users
	}
	return users[:max]
}

func (s *Server) renderPage(w http.ResponseWriter, page string, data interface{}) {
	tmpl, ok := s.pages[page]
	if !ok {
		slog.Error("template not found", "page", page)
		http.Error(w, "page not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout.html", data); err != nil {
		slog.Error("template render error", "page", page, "error", err)
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	mode := s.ops.Mode()

	// No mode chosen yet — show mode selection.
	if mode == "" {
		s.renderPage(w, "setup", struct {
			pageData
		}{
			pageData: pageData{Title: "Setup", Active: "index", Mode: mode},
		})
		return
	}

	cfg := s.ops.Config()
	relay := s.ops.GetRelayStatus()
	users, _ := s.ops.ListUsers()
	srvStatus := s.ops.ServerStatus()
	cliStatus := s.ops.ClientStatus()

	// Filter to registered users only and populate online status.
	online := s.ops.GetOnlineUsers()
	var registered []ops.UserInfo
	var onlineCount int
	for i := range users {
		if !users[i].Active {
			continue
		}
		if users[i].UUID != "" && online[users[i].UUID] {
			users[i].Online = true
			onlineCount++
		}
		registered = append(registered, users[i])
	}

	// Online users first.
	sort.Slice(registered, func(i, j int) bool {
		if registered[i].Online != registered[j].Online {
			return registered[i].Online
		}
		return registered[i].Name < registered[j].Name
	})

	data := struct {
		pageData
		Config        *config.Config
		ConfigPath    string
		Relay         ops.RelayStatus
		UserCount     int
		OnlineCount   int
		Users         []ops.UserInfo
		ServerStatus  ops.ServerStatus
		ClientStatus  ops.ClientStatus
		ConfigChanged bool
	}{
		pageData:      pageData{Title: "Status", Active: "index", Mode: mode},
		Config:        cfg,
		ConfigPath:    config.FilePath(),
		Relay:         relay,
		UserCount:     len(registered),
		OnlineCount:   onlineCount,
		Users:         registered,
		ServerStatus:  srvStatus,
		ClientStatus:  cliStatus,
		ConfigChanged: s.ops.ConfigChanged(),
	}
	s.renderPage(w, "index", data)
}

func (s *Server) handleRelay(w http.ResponseWriter, r *http.Request) {
	relay := s.ops.GetRelayStatus()
	mode := s.ops.Mode()

	data := struct {
		pageData
		Relay ops.RelayStatus
	}{
		pageData: pageData{Title: "Relay", Active: "relay", Mode: mode},
		Relay:    relay,
	}
	s.renderPage(w, "relay", data)
}

func (s *Server) handleRelayWizard(w http.ResponseWriter, r *http.Request) {
	cfg := s.ops.Config()
	providers := ops.CloudProviders()
	providersJSON, _ := json.Marshal(providers)
	mode := s.ops.Mode()

	data := struct {
		pageData
		Config        *config.Config
		ProvidersJSON template.JS
	}{
		pageData:      pageData{Title: "Provision Relay", Active: "relay", Mode: mode},
		Config:        cfg,
		ProvidersJSON: template.JS(providersJSON),
	}
	s.renderPage(w, "relay_wizard", data)
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.ops.ListUsers()
	if err != nil {
		slog.Error("listing users", "error", err)
	}
	mode := s.ops.Mode()
	relay := s.ops.GetRelayStatus()
	srvStatus := s.ops.ServerStatus()

	// Populate online status from relay stats.
	online := s.ops.GetOnlineUsers()
	var inactiveCount int
	for i := range users {
		if !users[i].Active {
			inactiveCount++
		}
		if users[i].UUID != "" && online[users[i].UUID] {
			users[i].Online = true
		}
	}

	// Sort: online first, then registered before unregistered, then alphabetical.
	sort.Slice(users, func(i, j int) bool {
		if users[i].Online != users[j].Online {
			return users[i].Online
		}
		if users[i].Active != users[j].Active {
			return users[i].Active
		}
		return users[i].Name < users[j].Name
	})

	data := struct {
		pageData
		Users         []ops.UserInfo
		RelayReady    bool
		ServerRunning bool
		InactiveCount int
	}{
		pageData:      pageData{Title: "Users", Active: "users", Mode: mode},
		Users:         users,
		RelayReady:    relay.Provisioned,
		ServerRunning: string(srvStatus.State) == "running",
		InactiveCount: inactiveCount,
	}
	s.renderPage(w, "users", data)
}

func (s *Server) handleUserNew(w http.ResponseWriter, r *http.Request) {
	mode := s.ops.Mode()
	relay := s.ops.GetRelayStatus()
	srvStatus := s.ops.ServerStatus()

	data := struct {
		pageData
		RelayReady    bool
		ServerRunning bool
	}{
		pageData:      pageData{Title: "Create User", Active: "users", Mode: mode},
		RelayReady:    relay.Provisioned,
		ServerRunning: string(srvStatus.State) == "running",
	}
	s.renderPage(w, "user_new", data)
}

func (s *Server) handleUserDetail(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/users/")
	if name == "" || name == "new" {
		http.NotFound(w, r)
		return
	}

	users, _ := s.ops.ListUsers()
	var found *ops.UserInfo
	for i, u := range users {
		if u.Name == name {
			found = &users[i]
			break
		}
	}

	if found == nil {
		http.NotFound(w, r)
		return
	}

	// Populate online status.
	if found.UUID != "" {
		online := s.ops.GetOnlineUsers()
		found.Online = online[found.UUID]
	}

	mode := s.ops.Mode()
	data := struct {
		pageData
		User ops.UserInfo
	}{
		pageData: pageData{Title: "User: " + name, Active: "users", Mode: mode},
		User:     *found,
	}
	s.renderPage(w, "user_detail", data)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	// Read from disk so we always show the actual file contents,
	// even if it was edited outside the dashboard.
	cfg, err := config.Load()
	if err != nil {
		cfg = s.ops.Config()
	}
	cfgYAML, _ := yaml.Marshal(cfg)
	mode := s.ops.Mode()

	running := string(s.ops.ServerStatus().State) == "running" ||
		string(s.ops.ClientStatus().State) == "running"

	logLevel := cfg.LogLevel
	if logLevel == "" {
		logLevel = "info"
	}

	data := struct {
		pageData
		ConfigPath string
		ConfigYAML string
		LogLevel   string
		Proxy      string
		Running    bool
	}{
		pageData:   pageData{Title: "Config", Active: "config", Mode: mode},
		ConfigPath: config.FilePath(),
		ConfigYAML: string(cfgYAML),
		LogLevel:   logLevel,
		Proxy:      cfg.Proxy,
		Running:    running,
	}
	s.renderPage(w, "config", data)
}
