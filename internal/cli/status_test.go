package cli

import (
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

func TestStatusWording(t *testing.T) {
	if got := workingStr(true); got != "working" {
		t.Errorf("workingStr(true) = %q, want \"working\"", got)
	}
	if got := workingStr(false); got != "not working" {
		t.Errorf("workingStr(false) = %q, want \"not working\"", got)
	}
	if got := yesNo(true); got != "yes" {
		t.Errorf("yesNo(true) = %q, want \"yes\"", got)
	}
	if got := yesNo(false); got != "no" {
		t.Errorf("yesNo(false) = %q, want \"no\"", got)
	}
	// Stored IP wins without any lookup; nothing to resolve without a domain.
	if got := relayIPDisplay("1.2.3.4", "relay.example.com"); got != "1.2.3.4" {
		t.Errorf("relayIPDisplay with stored IP = %q, want \"1.2.3.4\"", got)
	}
	if got := relayIPDisplay("", ""); got != "—" {
		t.Errorf("relayIPDisplay with nothing = %q, want dash", got)
	}
}

func TestStatusHeaderLines(t *testing.T) {
	relayCtx := &ops.ContextInfo{Name: "relay-tw-test", ID: "bef98b84", Role: "relay", Relay: "relay.tw.test", Current: true}
	out := strings.Join(statusHeaderLines(relayCtx, 3, "relay", "/etc/tw/config.yaml"), "\n")
	for _, want := range []string{"relay-tw-test", "bef98b84", "3 stored", "Mode:     relay", "Relay:    relay.tw.test", "/etc/tw/config.yaml"} {
		if !strings.Contains(out, want) {
			t.Errorf("relay header missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "User:") {
		t.Errorf("relay context must not print a User line:\n%s", out)
	}

	clientCtx := &ops.ContextInfo{Name: "alice", ID: "43c15a38", Role: "client", User: "alice", Relay: "relay.tw.test", Current: true}
	out = strings.Join(statusHeaderLines(clientCtx, 1, "client", "/etc/tw/config.yaml"), "\n")
	if !strings.Contains(out, "User:     alice") {
		t.Errorf("client header missing user line:\n%s", out)
	}

	out = strings.Join(statusHeaderLines(nil, 0, "", "/etc/tw/config.yaml"), "\n")
	if !strings.Contains(out, "not set up") {
		t.Errorf("empty header missing not-set-up hint:\n%s", out)
	}
	if !strings.Contains(out, "Mode:     —") {
		t.Errorf("empty header must show a dash mode:\n%s", out)
	}
}

func TestDaemonContextMismatch(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	cfg := config.Default()
	cfg.Mode = "server"
	cfg.Xray.RelayHost = "relay.example.com"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	// Daemon matches the active context: no warning.
	if w := daemonContextMismatch("server", "relay.example.com"); w != "" {
		t.Errorf("expected no warning when in sync, got: %q", w)
	}

	// Mode differs (daemon still on the old admin context).
	w := daemonContextMismatch("admin", "relay.example.com")
	if !strings.Contains(w, "mode") {
		t.Errorf("expected mode mismatch warning, got: %q", w)
	}

	// Relay differs.
	w = daemonContextMismatch("server", "other.example.com")
	if !strings.Contains(w, "relay") {
		t.Errorf("expected relay mismatch warning, got: %q", w)
	}

	// Empty daemon fields are treated as unknown, not a mismatch.
	if w := daemonContextMismatch("", ""); w != "" {
		t.Errorf("expected no warning for empty daemon status, got: %q", w)
	}
}
