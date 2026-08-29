package cli

import "testing"

// The gRPC control plane is unauthenticated, so it must bind loopback only —
// never 0.0.0.0. This locks that in.
func TestAPIListenAddrIsLoopback(t *testing.T) {
	got := apiListenAddr(50051)
	if got != "127.0.0.1:50051" {
		t.Fatalf("apiListenAddr(50051) = %q, want 127.0.0.1:50051", got)
	}
	// Must never produce an all-interfaces bind.
	if got == ":50051" || got == "0.0.0.0:50051" {
		t.Fatalf("apiListenAddr must not bind all interfaces, got %q", got)
	}
}
