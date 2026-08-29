package dashboard

import (
	"crypto/tls"
	"net/http"
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/ops"
)

func TestIsLoopbackHost(t *testing.T) {
	loop := []string{"127.0.0.1", "localhost", "::1", "127.0.0.5"}
	notLoop := []string{"", "0.0.0.0", "::", "192.168.1.10", "10.0.0.1", "203.0.113.4"}
	for _, h := range loop {
		if !isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = false, want true", h)
		}
	}
	for _, h := range notLoop {
		if isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = true, want false", h)
		}
	}
}

func TestIsRequestTLS(t *testing.T) {
	if isRequestTLS(&http.Request{}) {
		t.Error("plain HTTP request must not be treated as TLS")
	}
	if !isRequestTLS(&http.Request{TLS: &tls.ConnectionState{}}) {
		t.Error("direct TLS request must be treated as TLS")
	}
	fwd := &http.Request{Header: http.Header{"X-Forwarded-Proto": {"https"}}}
	if !isRequestTLS(fwd) {
		t.Error("X-Forwarded-Proto: https must be treated as TLS")
	}
}

// TestRunRefusesOffLoopbackWithoutOptIn is the SP-8 regression: the dashboard
// must not bind a non-loopback address over cleartext HTTP unless the operator
// has explicitly opted in.
func TestRunRefusesOffLoopbackWithoutOptIn(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o, err := ops.New()
	if err != nil {
		t.Fatal(err)
	}
	// Default config: DashboardAllowLAN is false.
	s := NewServer("0.0.0.0:8080", o)
	err = s.Run()
	if err == nil {
		t.Fatal("Run must refuse an off-loopback bind without the opt-in")
	}
	if !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("unexpected error: %v", err)
	}
}
