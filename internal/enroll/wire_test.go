package enroll

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWireRoundTrip(t *testing.T) {
	inv, _ := Mint("a1b2c3d4", time.Minute)
	h := NewHandler(inv, RoleOffer{Role: "server"})
	mux := http.NewServeMux()
	mux.Handle("/enroll/a1b2c3d4/", h)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	issuerErr := make(chan error, 1)
	go func() { // issuer side
		payload, err := h.AwaitOffer(context.Background())
		if err != nil {
			issuerErr <- err
			return
		}
		if string(payload) != `{"hello":"world"}` {
			issuerErr <- errStr("bad offer: " + string(payload))
			return
		}
		if h.SAS() == "" {
			issuerErr <- errStr("empty SAS")
			return
		}
		issuerErr <- h.Grant([]byte(`{"granted":true}`))
	}()

	var sawSAS string
	role, grant, err := RunEnrollee(context.Background(), srv.Client(),
		srv.URL+"/enroll/a1b2c3d4", inv.Code, "a1b2c3d4", EnrolleeCallbacks{
			MakeOffer: func(r RoleOffer) ([]byte, error) {
				if r.Role != "server" {
					t.Errorf("role = %q", r.Role)
				}
				return []byte(`{"hello":"world"}`), nil
			},
			ShowSAS: func(s string) { sawSAS = s },
		})
	if err != nil {
		t.Fatalf("enrollee: %v", err)
	}
	if err := <-issuerErr; err != nil {
		t.Fatalf("issuer: %v", err)
	}
	if role.Role != "server" || string(grant) != `{"granted":true}` {
		t.Fatalf("role=%v grant=%q", role, grant)
	}
	if !strings.Contains(sawSAS, "-") {
		t.Fatalf("SAS not shown: %q", sawSAS)
	}
	if err := h.AwaitCollected(context.Background()); err != nil {
		t.Fatalf("collected: %v", err)
	}
	// Invite burned: a second /start gets 410.
	resp, _ := srv.Client().Post(srv.URL+"/enroll/a1b2c3d4/start", "application/json",
		strings.NewReader(`{"pake":"AA=="}`))
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("second start: %d, want 410", resp.StatusCode)
	}
}

func TestWireDeny(t *testing.T) {
	inv, _ := Mint("a1b2c3d4", time.Minute)
	h := NewHandler(inv, RoleOffer{Role: "client", Username: "alice"})
	srv := httptest.NewServer(http.StripPrefix("", h))
	defer srv.Close()
	go func() {
		if _, err := h.AwaitOffer(context.Background()); err == nil {
			h.Deny()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _, err := RunEnrollee(ctx, srv.Client(), srv.URL, inv.Code, "a1b2c3d4", EnrolleeCallbacks{
		MakeOffer: func(RoleOffer) ([]byte, error) { return []byte(`{}`), nil },
		ShowSAS:   func(string) {},
	})
	if err == nil {
		t.Fatal("denied enrollment should error")
	}
}

type errStr string

func (e errStr) Error() string { return string(e) }
