package cli

import "testing"

func TestValidateCreateFlags(t *testing.T) {
	// No flags: interactive wizard, no error.
	nonInteractive, err := validateCreateFlags("", "", "")
	if err != nil || nonInteractive {
		t.Errorf("validateCreateFlags(\"\",\"\",\"\") = (%t, %v), want (false, nil)", nonInteractive, err)
	}
	// All manual flags: fully non-interactive.
	nonInteractive, err = validateCreateFlags("manual", "relay.example.com", "203.0.113.5")
	if err != nil || !nonInteractive {
		t.Errorf("all manual flags = (%t, %v), want (true, nil)", nonInteractive, err)
	}
	// Partial flags: still wizard mode (flags pre-answer prompts), no error.
	nonInteractive, err = validateCreateFlags("manual", "relay.example.com", "")
	if err != nil || nonInteractive {
		t.Errorf("partial flags = (%t, %v), want (false, nil)", nonInteractive, err)
	}
	nonInteractive, err = validateCreateFlags("", "relay.example.com", "")
	if err != nil || nonInteractive {
		t.Errorf("domain only = (%t, %v), want (false, nil)", nonInteractive, err)
	}
	// Unknown provider name is rejected.
	if _, err = validateCreateFlags("hetzner", "relay.example.com", "203.0.113.5"); err == nil {
		t.Error("provider hetzner = nil error, want error (only manual supported)")
	}
	// --ip without --provider manual is rejected.
	if _, err = validateCreateFlags("", "", "203.0.113.5"); err == nil {
		t.Error("ip without provider = nil error, want error")
	}
}
