package ops

import "testing"

func TestValidateRelayName(t *testing.T) {
	ok := []string{"n", "relay-1a2b", "relay-abcd", "a1", "web01"}
	bad := []string{"", "-relay", "relay-", "Relay", "a b", `x"`, "x\n}", "a$(id)", "toolongtoolongtoolongtoolongtoolong"}
	for _, s := range ok {
		if err := validateRelayName(s); err != nil {
			t.Errorf("validateRelayName(%q) unexpected error: %v", s, err)
		}
	}
	for _, s := range bad {
		if err := validateRelayName(s); err == nil {
			t.Errorf("validateRelayName(%q) should have errored", s)
		}
	}
}

func TestValidateSSHUser(t *testing.T) {
	ok := []string{"ubuntu", "tw", "_svc", "user-1", "a_b-c"}
	bad := []string{"", "1user", "Ubuntu", "u ser", "root;rm", "a$(x)", `x"`, "user\nx"}
	for _, s := range ok {
		if err := validateSSHUser(s); err != nil {
			t.Errorf("validateSSHUser(%q) unexpected error: %v", s, err)
		}
	}
	for _, s := range bad {
		if err := validateSSHUser(s); err == nil {
			t.Errorf("validateSSHUser(%q) should have errored", s)
		}
	}
}

func TestValidateHostname(t *testing.T) {
	ok := []string{"ntw.test", "relay.example.com", "localhost", "a.b.c.d", "xn--hxajbheg2az3al.example"}
	bad := []string{"", "-bad.com", "bad-.com", "a..b", "has space.com", `evil"$(id)`, "x\ny.com", "under_score.com"}
	for _, s := range ok {
		if err := validateHostname(s); err != nil {
			t.Errorf("validateHostname(%q) unexpected error: %v", s, err)
		}
	}
	for _, s := range bad {
		if err := validateHostname(s); err == nil {
			t.Errorf("validateHostname(%q) should have errored", s)
		}
	}
}
