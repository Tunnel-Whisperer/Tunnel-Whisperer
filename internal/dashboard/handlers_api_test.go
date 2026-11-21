package dashboard

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/pki"
)

func TestAPIUserActionRejectsInvalidName(t *testing.T) {
	s := newClientDashboardForTest(t, "server")

	cases := []struct {
		method, target, body string
	}{
		{http.MethodDelete, "/api/users/%2e%2e", ""},
		{http.MethodDelete, "/api/users/a%20b", ""},
		{http.MethodDelete, "/api/users/%2e%2e%5cetc", ""},
		{http.MethodPut, "/api/users/%2e%2e/single-session", `{"enabled":true}`},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.target, strings.NewReader(c.body))
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid user name") {
			t.Errorf("%s %s: status %d, body %q; want 400 invalid user name", c.method, c.target, rec.Code, rec.Body.String())
		}
	}
}

func TestAPIDestroyRelayRejectsMalformedBody(t *testing.T) {
	s := newClientDashboardForTest(t, "server")

	req := httptest.NewRequest(http.MethodPost, "/api/relay/destroy", strings.NewReader(`{"creds":`))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, body %q; want 400", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "session_id") {
		t.Fatalf("destroy session started despite malformed body: %s", rec.Body.String())
	}
}

func TestUsersPageShowsExpiry(t *testing.T) {
	s := newClientDashboardForTest(t, "server")

	caCert, caKey, err := pki.GenerateCA("srv-1")
	if err != nil {
		t.Fatal(err)
	}
	_, csr, err := pki.GenerateKeyAndCSR("x")
	if err != nil {
		t.Fatal(err)
	}
	seed := func(name string, notAfter time.Time) {
		crt, err := pki.SignClientCSR(caCert, caKey, csr, "srv-1", notAfter)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(config.UsersDir(), name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "client.crt"), crt, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	future := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second).UTC()
	seed("alice", future)
	seed("old", time.Now().Add(-time.Hour))
	if err := os.MkdirAll(filepath.Join(config.UsersDir(), "plain"), 0o700); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/users", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, body)
	}
	for _, want := range []string{
		`<th class="sortable" data-sort="expires">Expires</th>`,
		future.Format(time.RFC3339),
		`<span class="badge badge-red">expired</span>`,
		`data-expires="` + future.Format(time.RFC3339) + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("users page missing %q", want)
		}
	}
	if n := strings.Count(body, `badge-red">expired`); n != 1 {
		t.Errorf("expired badges = %d, want 1", n)
	}
}

func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
		t.Setenv(k, "")
	}
}

func TestAPISetProxyRedactsResponse(t *testing.T) {
	clearProxyEnv(t)
	s := newClientDashboardForTest(t, "client")

	req := httptest.NewRequest(http.MethodPost, "/api/proxy", strings.NewReader(`{"proxy":"socks5://u:secret@h:1080"}`))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, body)
	}
	if !strings.Contains(body, `"proxy":"socks5://redacted@h:1080"`) {
		t.Fatalf("response must carry the redacted normalised proxy URL: %s", body)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/proxy", strings.NewReader(`{"proxy":""}`))
	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"proxy":""`) {
		t.Fatalf("clear: status %d, body %q; want 200 with empty proxy", rec.Code, rec.Body.String())
	}
}

func TestConfigPageShowsEnvProxy(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "http://envproxy:3128")
	s := newClientDashboardForTest(t, "client")

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, body)
	}
	for _, want := range []string{
		"source: env HTTPS_PROXY",
		`<span class="badge badge-green" id="proxy-badge">environment</span>`,
		"set a proxy here to override it",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("config page missing %q", want)
		}
	}
}

func writeProxyConfig(t *testing.T, s *Server, proxy, uuid string) {
	t.Helper()
	cfgYAML := "mode: client\nproxy: " + proxy + "\nxray:\n  uuid: " + uuid + "\n"
	if err := os.WriteFile(config.FilePath(), []byte(cfgYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.ops.ReloadConfig(); err != nil {
		t.Fatal(err)
	}
}

func TestConfigPageRedactsSecrets(t *testing.T) {
	clearProxyEnv(t)
	s := newClientDashboardForTest(t, "client")
	const uuid = "0b7a3c52-9f1e-4d6b-8a2c-5e4f3d2c1b0a"
	writeProxyConfig(t, s, "http://u:secret@p:3128", uuid)

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, body)
	}
	for _, leak := range []string{"secret", uuid} {
		if strings.Contains(body, leak) {
			t.Errorf("config page leaks %q", leak)
		}
	}
	if !strings.Contains(body, `value="http://redacted@p:3128"`) {
		t.Errorf("proxy input missing redacted URL")
	}
	pre := body[strings.Index(body, "<pre>"):]
	for _, want := range []string{"proxy: http://redacted@p:3128", "uuid: redacted"} {
		if !strings.Contains(pre, want) {
			t.Errorf("config YAML missing %q", want)
		}
	}

	raw, err := os.ReadFile(config.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "u:secret@p:3128") || !strings.Contains(string(raw), uuid) {
		t.Errorf("config file must keep the real secrets: %s", raw)
	}
}

func postProxy(s *Server, proxy string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/proxy", strings.NewReader(`{"proxy":"`+proxy+`"}`))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

func TestAPISetProxyKeepsRedactedCredentials(t *testing.T) {
	clearProxyEnv(t)
	s := newClientDashboardForTest(t, "client")
	writeProxyConfig(t, s, "http://u:secret@p:3128", "")

	rec := postProxy(s, "http://redacted@p2:3128")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("response leaks the password: %s", rec.Body.String())
	}
	if got := s.ops.Config().Proxy; got != "http://u:secret@p2:3128" {
		t.Fatalf("stored proxy = %q, want http://u:secret@p2:3128", got)
	}

	rec = postProxy(s, "http://a:b@p:1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if got := s.ops.Config().Proxy; got != "http://a:b@p:1" {
		t.Fatalf("stored proxy = %q, want http://a:b@p:1", got)
	}

	// Only the username "redacted" is the placeholder; a real username with
	// the literal password "redacted" is stored as typed.
	rec = postProxy(s, "http://a:redacted@p:1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if got := s.ops.Config().Proxy; got != "http://a:redacted@p:1" {
		t.Fatalf("stored proxy = %q, want http://a:redacted@p:1", got)
	}
}

func TestAPISetProxyRedactedWithoutStoredCredentials(t *testing.T) {
	clearProxyEnv(t)
	s := newClientDashboardForTest(t, "client")
	writeProxyConfig(t, s, "http://p:3128", "")

	rec := postProxy(s, "http://redacted@p:3128")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "stored proxy has no credentials to keep") {
		t.Fatalf("status %d, body %q; want 400 no credentials", rec.Code, rec.Body.String())
	}
	if got := s.ops.Config().Proxy; got != "http://p:3128" {
		t.Fatalf("stored proxy changed to %q", got)
	}
}

func TestAPIConfigRedactsSecrets(t *testing.T) {
	clearProxyEnv(t)
	s := newClientDashboardForTest(t, "client")
	const uuid = "0b7a3c52-9f1e-4d6b-8a2c-5e4f3d2c1b0a"
	writeProxyConfig(t, s, "http://u:secret@p:3128", uuid)

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, body)
	}
	for _, leak := range []string{"secret", uuid} {
		if strings.Contains(body, leak) {
			t.Errorf("/api/config leaks %q", leak)
		}
	}
	if !strings.Contains(body, "redacted@") {
		t.Errorf("/api/config missing redacted proxy: %s", body)
	}
}
