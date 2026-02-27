package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/tunnelwhisperer/tw/internal/ops"
)

func jsonOK(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ── Read-only endpoints ─────────────────────────────────────────────────────

func (s *Server) apiStatus(w http.ResponseWriter, r *http.Request) {
	mode := s.ops.Mode()
	relay := s.ops.GetRelayStatus()
	users, _ := s.ops.ListUsers()

	// Count only registered users (those applied to the relay).
	registeredCount := 0
	for _, u := range users {
		if u.Active {
			registeredCount++
		}
	}

	resp := map[string]interface{}{
		"mode":           mode,
		"version":        "0.1.0-dev",
		"relay":          relay,
		"user_count":     registeredCount,
		"config_changed": s.ops.ConfigChanged(),
	}

	if mode == "server" {
		resp["server"] = s.ops.ServerStatus()

		online := s.ops.GetOnlineUsers()
		onlineUUIDs := make([]string, 0, len(online))
		for uuid := range online {
			onlineUUIDs = append(onlineUUIDs, uuid)
		}
		resp["online"] = onlineUUIDs
	}
	if mode == "client" {
		resp["client"] = s.ops.ClientStatus()
	}

	jsonOK(w, resp)
}

func (s *Server) apiConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.ops.Config()
	jsonOK(w, cfg)
}

func (s *Server) apiProviders(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, ops.CloudProviders())
}

func (s *Server) apiRelay(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, s.ops.GetRelayStatus())
}

// ── Mode ─────────────────────────────────────────────────────────────────────

func (s *Server) apiSetMode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := s.ops.SetMode(req.Mode); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	jsonOK(w, map[string]string{"mode": req.Mode})
}

// ── Server start/stop ────────────────────────────────────────────────────────

func (s *Server) apiServerStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID, progress := s.sse.create()

	go func() {
		if err := s.ops.StartServer(progress); err != nil {
			slog.Error("server start failed", "error", err)
		}
	}()

	jsonOK(w, map[string]string{"session_id": sessionID})
}

func (s *Server) apiServerStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID, progress := s.sse.create()

	go func() {
		if err := s.ops.StopServer(progress); err != nil {
			slog.Error("server stop failed", "error", err)
		}
	}()

	jsonOK(w, map[string]string{"session_id": sessionID})
}

func (s *Server) apiServerRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID, progress := s.sse.create()

	go func() {
		if err := s.ops.RestartServer(progress); err != nil {
			slog.Error("server restart failed", "error", err)
		}
	}()

	jsonOK(w, map[string]string{"session_id": sessionID})
}

// ── Client start/stop/upload ─────────────────────────────────────────────────

func (s *Server) apiClientStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID, progress := s.sse.create()

	go func() {
		if err := s.ops.StartClient(progress); err != nil {
			slog.Error("client start failed", "error", err)
		}
	}()

	jsonOK(w, map[string]string{"session_id": sessionID})
}

func (s *Server) apiClientStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID, progress := s.sse.create()

	go func() {
		if err := s.ops.StopClient(progress); err != nil {
			slog.Error("client stop failed", "error", err)
		}
	}()

	jsonOK(w, map[string]string{"session_id": sessionID})
}

func (s *Server) apiClientReconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID, progress := s.sse.create()

	go func() {
		if err := s.ops.ReconnectClient(progress); err != nil {
			slog.Error("client reconnect failed", "error", err)
		}
	}()

	jsonOK(w, map[string]string{"session_id": sessionID})
}

func (s *Server) apiClientUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Accept multipart form with a "config" file field.
	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10 MB max
		jsonError(w, "invalid multipart form", http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("config")
	if err != nil {
		jsonError(w, "missing 'config' file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		jsonError(w, "reading uploaded file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := s.ops.UploadClientConfig(data); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	jsonOK(w, map[string]string{"status": "ok"})
}

// ── Relay endpoints ──────────────────────────────────────────────────────────

func (s *Server) apiTestCreds(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ProviderName string `json:"provider_name"`
		Token        string `json:"token"`
		AWSSecretKey string `json:"aws_secret_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := s.ops.TestCloudCredentials(req.ProviderName, req.Token, req.AWSSecretKey); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	jsonOK(w, map[string]string{"status": "ok"})
}

func (s *Server) apiProvisionRelay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ops.RelayProvisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	sessionID, progress := s.sse.create()

	go func() {
		if err := s.ops.ProvisionRelay(context.Background(), req, progress); err != nil {
			slog.Error("relay provision failed", "error", err)
		}
	}()

	jsonOK(w, map[string]string{"session_id": sessionID})
}

func (s *Server) apiDestroyRelay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Creds map[string]string `json:"creds"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	sessionID, progress := s.sse.create()

	go func() {
		if err := s.ops.DestroyRelay(context.Background(), req.Creds, progress); err != nil {
			slog.Error("relay destroy failed", "error", err)
		}
	}()

	jsonOK(w, map[string]string{"session_id": sessionID})
}

func (s *Server) apiTestRelay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID, progress := s.sse.create()

	go func() {
		s.ops.TestRelay(progress)
	}()

	jsonOK(w, map[string]string{"session_id": sessionID})
}

func (s *Server) apiGenerateScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	script, err := s.ops.GenerateManualInstallScript(req.Domain)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]string{"script": script})
}

func (s *Server) apiSaveManualRelay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Domain string `json:"domain"`
		IP     string `json:"ip"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.IP == "" {
		jsonError(w, "IP address is required", http.StatusBadRequest)
		return
	}

	if err := s.ops.SaveManualRelay(req.Domain, req.IP); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]string{"status": "ok"})
}

// ── User endpoints ───────────────────────────────────────────────────────────

func (s *Server) apiUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		users, err := s.ops.ListUsers()
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, users)

	case http.MethodPost:
		var req ops.CreateUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request body", http.StatusBadRequest)
			return
		}

		sessionID, progress := s.sse.create()

		go func() {
			if err := s.ops.CreateUser(context.Background(), req, progress); err != nil {
				slog.Error("user creation failed", "error", err)
			}
		}()

		jsonOK(w, map[string]string{"session_id": sessionID})

	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) apiUserAction(w http.ResponseWriter, r *http.Request) {
	// Routes: DELETE /api/users/{name}, GET /api/users/{name}/download
	path := strings.TrimPrefix(r.URL.Path, "/api/users/")
	parts := strings.SplitN(path, "/", 2)
	name := parts[0]

	if name == "" {
		jsonError(w, "user name required", http.StatusBadRequest)
		return
	}

	if len(parts) == 2 && parts[1] == "download" {
		s.apiUserDownload(w, r, name)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		if err := s.ops.DeleteUser(name); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]string{"status": "deleted"})

	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) apiApplyUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Names []string `json:"names"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	sessionID, progress := s.sse.create()

	go func() {
		if err := s.ops.ApplyUsers(context.Background(), req.Names, progress); err != nil {
			slog.Error("apply users failed", "error", err)
		}
	}()

	jsonOK(w, map[string]string{"session_id": sessionID})
}

func (s *Server) apiUnregisterUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Names []string `json:"names"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	sessionID, progress := s.sse.create()

	go func() {
		if err := s.ops.UnregisterUsers(context.Background(), req.Names, progress); err != nil {
			slog.Error("unregister users failed", "error", err)
		}
	}()

	jsonOK(w, map[string]string{"session_id": sessionID})
}

func (s *Server) apiOnlineUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	online := s.ops.GetOnlineUsers()
	uuids := make([]string, 0, len(online))
	for uuid := range online {
		uuids = append(uuids, uuid)
	}
	jsonOK(w, map[string]interface{}{"online": uuids})
}

func (s *Server) apiUserDownload(w http.ResponseWriter, r *http.Request, name string) {
	data, err := s.ops.GetUserConfigBundle(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"-tw-config.zip\"")
	w.Write(data)
}

// ── Proxy ────────────────────────────────────────────────────────────────────

func (s *Server) apiSetProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Proxy string `json:"proxy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := s.ops.SetProxy(req.Proxy); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	jsonOK(w, map[string]string{"status": "ok", "proxy": req.Proxy})
}

// ── Log level ────────────────────────────────────────────────────────────────

func (s *Server) apiSetLogLevel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		LogLevel string `json:"log_level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := s.ops.SetLogLevel(req.LogLevel); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	jsonOK(w, map[string]string{"status": "ok", "log_level": req.LogLevel})
}

// ── Log streaming ───────────────────────────────────────────────────────────

func (s *Server) apiLogs(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Send buffered history first.
	for _, entry := range s.logs.snapshot() {
		data, _ := json.Marshal(entry)
		fmt.Fprintf(w, "data: %s\n\n", data)
	}
	flusher.Flush()

	// Stream new entries.
	ch, unsub := s.logs.subscribe()
	defer unsub()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(entry)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
