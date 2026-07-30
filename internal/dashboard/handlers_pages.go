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
	"github.com/tunnelwhisperer/tw/internal/version"
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
			pageData: s.newPageData("Setup", "index"),
		})
		return
	}

	// Relay mode: relay-management landing. Shows a relay-status summary and
	// links to the full relay management page (/relay). Server admission and
	// the richer multi-tenant views land in later phases.
	if mode == "relay" {
		relay := s.ops.GetRelayStatus()
		s.renderPage(w, "relay_home", struct {
			pageData
			Relay ops.RelayStatus
		}{
			pageData: s.newPageData("Relay", "index"),
			Relay:    relay,
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
		StatsEnabled  bool
		ClientTunnels []config.Tunnel
	}{
		pageData:      s.newPageData("Status", "index"),
		Config:        cfg,
		ConfigPath:    config.FilePath(),
		Relay:         relay,
		UserCount:     len(registered),
		OnlineCount:   onlineCount,
		Users:         registered,
		ServerStatus:  srvStatus,
		ClientStatus:  cliStatus,
		ConfigChanged: s.ops.ConfigChanged(),
		StatsEnabled:  s.ops.StatsEnabled(),
		// Persisted overrides only (no runtime --map state to show here).
		ClientTunnels: cfg.Client.EffectiveTunnels(nil),
	}
	s.renderPage(w, "index", data)
}

func (s *Server) handleRelay(w http.ResponseWriter, r *http.Request) {
	relay := s.ops.GetRelayStatus()

	data := struct {
		pageData
		Relay ops.RelayStatus
	}{
		pageData: s.newPageData("Relay", "relay"),
		Relay:    relay,
	}
	s.renderPage(w, "relay", data)
}

func (s *Server) handleRelayWizard(w http.ResponseWriter, r *http.Request) {
	cfg := s.ops.Config()
	providers := ops.CloudProviders()
	providersJSON, _ := json.Marshal(providers)

	data := struct {
		pageData
		Config        *config.Config
		ProvidersJSON template.JS
	}{
		pageData:      s.newPageData("Provision Relay", "relay"),
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
		pageData:      s.newPageData("Users", "users"),
		Users:         users,
		RelayReady:    relay.Provisioned,
		ServerRunning: string(srvStatus.State) == "running",
		InactiveCount: inactiveCount,
	}
	s.renderPage(w, "users", data)
}

func (s *Server) handleUserNew(w http.ResponseWriter, r *http.Request) {
	relay := s.ops.GetRelayStatus()
	srvStatus := s.ops.ServerStatus()
	apps := s.ops.ListApplications()
	appsJSON, _ := json.Marshal(apps)

	// Support ?from=username to pre-fill mappings from an existing user.
	var prefillJSON template.JS = "null"
	if fromName := r.URL.Query().Get("from"); fromName != "" {
		users, _ := s.ops.ListUsers()
		for _, u := range users {
			if u.Name == fromName {
				mappings := make([]config.PortMapping, len(u.Tunnels))
				for i, t := range u.Tunnels {
					mappings[i] = config.PortMapping{ClientPort: t.LocalPort, ServerPort: t.RemotePort}
				}
				data, _ := json.Marshal(mappings)
				prefillJSON = template.JS(data)
				break
			}
		}
	}

	data := struct {
		pageData
		RelayReady    bool
		ServerRunning bool
		AppsJSON      template.JS
		PrefillJSON   template.JS
	}{
		pageData:      s.newPageData("Create User", "users"),
		RelayReady:    relay.Provisioned,
		ServerRunning: string(srvStatus.State) == "running",
		AppsJSON:      template.JS(appsJSON),
		PrefillJSON:   prefillJSON,
	}
	s.renderPage(w, "user_new", data)
}

func (s *Server) handleUserDetail(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/users/")
	if path == "" || path == "new" {
		http.NotFound(w, r)
		return
	}

	// Check for /users/{name}/edit pattern.
	var editing bool
	name := path
	if parts := strings.SplitN(path, "/", 2); len(parts) == 2 && parts[1] == "edit" {
		name = parts[0]
		editing = true
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


	if editing {
		apps := s.ops.ListApplications()
		appsJSON, _ := json.Marshal(apps)
		data := struct {
			pageData
			User     ops.UserInfo
			AppsJSON template.JS
		}{
			pageData: s.newPageData("Edit: " + name, "users"),
			User:     *found,
			AppsJSON: template.JS(appsJSON),
		}
		s.renderPage(w, "user_edit", data)
		return
	}

	// Populate online status.
	if found.UUID != "" {
		online := s.ops.GetOnlineUsers()
		found.Online = online[found.UUID]
	}

	data := struct {
		pageData
		User         ops.UserInfo
		StatsEnabled bool
	}{
		pageData:     s.newPageData("User: " + name, "users"),
		User:         *found,
		StatsEnabled: s.ops.StatsEnabled(),
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

	running := string(s.ops.ServerStatus().State) == "running" ||
		string(s.ops.ClientStatus().State) == "running"

	logLevel := cfg.LogLevel
	if logLevel == "" {
		logLevel = "info"
	}

	historySize := cfg.Analytics.HistorySize
	if historySize <= 0 {
		historySize = 720
	}

	data := struct {
		pageData
		ConfigPath       string
		ConfigYAML       string
		LogLevel         string
		Proxy            string
		Running          bool
		Server           config.ServerConfig
		Client           config.ClientConfig
		Xray             config.XrayConfig
		AnalyticsEnabled bool
		HistorySize      int
		Version          string
	}{
		pageData:         s.newPageData("Config", "config"),
		ConfigPath:       config.FilePath(),
		ConfigYAML:       string(cfgYAML),
		LogLevel:         logLevel,
		Proxy:            cfg.Proxy,
		Running:          running,
		Server:           cfg.Server,
		Client:           cfg.Client,
		Xray:             cfg.Xray,
		AnalyticsEnabled: cfg.Analytics.Enabled,
		HistorySize:      historySize,
		Version:          version.Version,
	}
	s.renderPage(w, "config", data)
}

func (s *Server) handleBandwidth(w http.ResponseWriter, r *http.Request) {
	data := struct {
		pageData
		StatsEnabled bool
	}{
		pageData:     s.newPageData("Bandwidth", "bandwidth"),
		StatsEnabled: s.ops.StatsEnabled(),
	}
	s.renderPage(w, "bandwidth", data)
}

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	apps := s.ops.ListApplications()

	data := struct {
		pageData
		Apps []config.Application
	}{
		pageData: s.newPageData("Applications", "apps"),
		Apps:     apps,
	}
	s.renderPage(w, "apps", data)
}

func (s *Server) handleAppNew(w http.ResponseWriter, r *http.Request) {

	data := struct {
		pageData
	}{
		pageData: s.newPageData("Create Application", "apps"),
	}
	s.renderPage(w, "app_new", data)
}

func (s *Server) handleAppEdit(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/apps/edit/")
	if name == "" {
		http.NotFound(w, r)
		return
	}

	apps := s.ops.ListApplications()
	var found *config.Application
	for i, a := range apps {
		if a.Name == name {
			found = &apps[i]
			break
		}
	}
	if found == nil {
		http.NotFound(w, r)
		return
	}

	data := struct {
		pageData
		App config.Application
	}{
		pageData: s.newPageData("Edit Application", "apps"),
		App:      *found,
	}
	s.renderPage(w, "app_edit", data)
}

// handleServers is the relay-mode tenant page; other modes bounce home.
func (s *Server) handleServers(w http.ResponseWriter, r *http.Request) {
	if s.ops.Mode() != "relay" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.renderPage(w, "servers", struct {
		pageData
	}{
		pageData: s.newPageData("Servers", "servers"),
	})
}
