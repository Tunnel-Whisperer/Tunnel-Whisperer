package dashboard

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

func newClientDashboardForTest(t *testing.T, mode string) *Server {
	t.Helper()
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgYAML := "mode: " + mode + `
client:
  tunnels:
    - local_port: 8080
      remote_host: 127.0.0.1
      remote_port: 15432
    - local_port: 9090
      remote_host: 127.0.0.1
      remote_port: 15433
`
	if err := os.WriteFile(config.FilePath(), []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	o, err := ops.New()
	if err != nil {
		t.Fatal(err)
	}
	return NewServer("127.0.0.1:0", o)
}

func postOverride(s *Server, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/client/port-override", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

func TestAPIPortOverrideSetAndClear(t *testing.T) {
	s := newClientDashboardForTest(t, "client")

	rec := postOverride(s, `{"server_port":15432,"local_port":4000}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set: status %d, body %s", rec.Code, rec.Body.String())
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Client.PortOverrides[15432] != 4000 {
		t.Errorf("override not persisted: %v", cfg.Client.PortOverrides)
	}

	rec = postOverride(s, `{"server_port":15432,"clear":true}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"cleared":true`) {
		t.Fatalf("clear: status %d, body %s", rec.Code, rec.Body.String())
	}
	rec = postOverride(s, `{"server_port":15432,"clear":true}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"cleared":false`) {
		t.Fatalf("clear-nothing: status %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestAPIPortOverrideValidationError(t *testing.T) {
	s := newClientDashboardForTest(t, "client")
	// 9090 duplicates tunnel 15433's default effective port.
	rec := postOverride(s, `{"server_port":15432,"local_port":9090}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("duplicate: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	rec = postOverride(s, `{"server_port":2222,"local_port":4000}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown server port: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestAPIPortOverrideModeAndMethod(t *testing.T) {
	s := newClientDashboardForTest(t, "server")
	rec := postOverride(s, `{"server_port":15432,"local_port":4000}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("server mode: want 400, got %d", rec.Code)
	}

	s2 := newClientDashboardForTest(t, "client")
	req := httptest.NewRequest(http.MethodGet, "/api/client/port-override", nil)
	rec2 := httptest.NewRecorder()
	s2.mux.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: want 405, got %d", rec2.Code)
	}
}
