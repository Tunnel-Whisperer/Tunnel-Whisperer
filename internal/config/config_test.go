package config

import "testing"

func TestValidMode(t *testing.T) {
	for _, m := range []string{"server", "client", "admin"} {
		if !ValidMode(m) {
			t.Errorf("ValidMode(%q) = false, want true", m)
		}
	}
	for _, m := range []string{"", "Server", "relay", "ADMIN", "x"} {
		if ValidMode(m) {
			t.Errorf("ValidMode(%q) = true, want false", m)
		}
	}
}
