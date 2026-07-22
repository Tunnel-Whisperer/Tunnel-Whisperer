package cli

import "testing"

func TestModeError(t *testing.T) {
	// Unset mode is always allowed (setup not done yet).
	if err := modeError("", []string{"server"}); err != nil {
		t.Errorf("modeError(\"\", [server]) = %v, want nil", err)
	}
	// Current mode present in the allow-list is permitted.
	if err := modeError("relay", []string{"relay", "server"}); err != nil {
		t.Errorf("modeError(relay, [relay server]) = %v, want nil", err)
	}
	if err := modeError("server", []string{"server"}); err != nil {
		t.Errorf("modeError(server, [server]) = %v, want nil", err)
	}
	// Current mode absent from the allow-list is rejected.
	if err := modeError("client", []string{"server"}); err == nil {
		t.Error("modeError(client, [server]) = nil, want error")
	}
	if err := modeError("server", []string{"client", "relay"}); err == nil {
		t.Error("modeError(server, [client relay]) = nil, want error")
	}
	// Relay ownership moved to relay mode: server mode must be refused the
	// relay-gated relay commands (e.g. `tw relay create`), and relay allowed.
	if err := modeError("server", []string{"relay"}); err == nil {
		t.Error("modeError(server, [relay]) = nil, want error (server must not run `tw relay create`)")
	}
	if err := modeError("relay", []string{"relay"}); err != nil {
		t.Errorf("modeError(relay, [relay]) = %v, want nil", err)
	}
}
