package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
