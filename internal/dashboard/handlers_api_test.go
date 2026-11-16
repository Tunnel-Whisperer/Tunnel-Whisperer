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
