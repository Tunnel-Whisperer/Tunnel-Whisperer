package cli

import "testing"

func TestModeError(t *testing.T) {
	// Unset mode is always allowed (setup not done yet).
	if err := modeError("", []string{"server"}); err != nil {
		t.Errorf("modeError(\"\", [server]) = %v, want nil", err)
	}
	// Current mode present in the allow-list is permitted.
	if err := modeError("admin", []string{"admin", "server"}); err != nil {
		t.Errorf("modeError(admin, [admin server]) = %v, want nil", err)
	}
	if err := modeError("server", []string{"server"}); err != nil {
		t.Errorf("modeError(server, [server]) = %v, want nil", err)
	}
	// Current mode absent from the allow-list is rejected.
	if err := modeError("client", []string{"server"}); err == nil {
		t.Error("modeError(client, [server]) = nil, want error")
	}
	if err := modeError("server", []string{"client", "admin"}); err == nil {
		t.Error("modeError(server, [client admin]) = nil, want error")
	}
	// Relay ownership moved to admin mode: server mode must be refused the
	// admin-gated relay commands (e.g. `tw admin create`), and admin allowed.
	if err := modeError("server", []string{"admin"}); err == nil {
		t.Error("modeError(server, [admin]) = nil, want error (server must not run `tw admin create`)")
	}
	if err := modeError("admin", []string{"admin"}); err != nil {
		t.Errorf("modeError(admin, [admin]) = %v, want nil", err)
	}
}
