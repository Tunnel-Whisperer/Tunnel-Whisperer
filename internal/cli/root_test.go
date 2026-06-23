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
}
