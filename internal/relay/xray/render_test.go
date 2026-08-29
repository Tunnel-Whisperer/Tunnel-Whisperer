package xray

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderConfigPerTenant(t *testing.T) {
	out, err := RenderConfig(Config{Tenants: []Tenant{
		{ServerID: "web-01-a1b2c3d4", UUID: "11111111-1111-1111-1111-111111111111", RemotePort: 20000},
		{ServerID: "db-02-99887766", UUID: "22222222-2222-2222-2222-222222222222", RemotePort: 20001},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	for _, want := range []string{
		`"tag": "vless-in-web-01-a1b2c3d4"`,
		`"port": 30000`,                                  // 20000 + 10000
		`"tag": "vless-in-db-02-99887766"`,
		`"port": 30001`,
		`"path": "/tw/web-01-a1b2c3d4"`,
		`"id": "11111111-1111-1111-1111-111111111111"`,
		`"ruleTag": "allow-web-01-a1b2c3d4"`,
		`"port": "22,20000"`,
		`"ruleTag": "deny-web-01-a1b2c3d4"`,
		`"tag": "blackhole"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n---\n%s", want, out)
		}
	}
	// First-match-wins: the allow rule must precede its deny rule.
	allowAt := strings.Index(out, "allow-web-01-a1b2c3d4")
	denyAt := strings.Index(out, "deny-web-01-a1b2c3d4")
	if allowAt < 0 || denyAt < 0 || allowAt >= denyAt {
		t.Errorf("allow rule must precede deny rule (allow=%d deny=%d)", allowAt, denyAt)
	}
}

// TestRenderConfigFreedomLoopbackOnly: the relay's whole job is
// vless → freedom → 127.0.0.1:<port>. Since Xray 26.3–26.6 the freedom outbound
// blocks loopback/private targets by default (anti-SSRF), so it needs an
// explicit finalRules allow for loopback. But the upstream default still lets
// PUBLIC destinations through, which turned the relay into an open outbound
// proxy for authenticated tenants (finding SP-5). A terminal block after the
// loopback allow makes freedom default-DENY: only 127.0.0.1 egresses, every
// other destination (public or private) is blocked. Per-tenant port
// restrictions stay enforced by the routing allow/deny rules.
func TestRenderConfigFreedomLoopbackOnly(t *testing.T) {
	out, err := RenderConfig(Config{Tenants: []Tenant{
		{ServerID: "web-01-a1b2c3d4", UUID: "11111111-1111-1111-1111-111111111111", RemotePort: 20000},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Loopback is allowed...
	for _, want := range []string{`"finalRules"`, `"action": "allow"`, `"127.0.0.1/32"`} {
		if !strings.Contains(out, want) {
			t.Errorf("freedom outbound missing loopback allow (%q)\n---\n%s", want, out)
		}
	}
	// ...and everything else is blocked (no open proxy).
	for _, want := range []string{`"action": "block"`, `"0.0.0.0/0"`, `"::/0"`} {
		if !strings.Contains(out, want) {
			t.Errorf("freedom outbound missing terminal block (%q) — relay would be an open proxy\n---\n%s", want, out)
		}
	}
	// The allow must precede the block, or the loopback path would be blocked too.
	if strings.Index(out, `"127.0.0.1/32"`) > strings.Index(out, `"0.0.0.0/0"`) {
		t.Error("loopback allow must come before the terminal block in finalRules")
	}
}

func TestRenderConfigRequiresTenant(t *testing.T) {
	if _, err := RenderConfig(Config{}); err == nil {
		t.Error("expected error with no tenants")
	}
}

func TestRenderConfigRejectsBadInput(t *testing.T) {
	cases := map[string]Config{
		"server id with quote": {Tenants: []Tenant{
			{ServerID: `web"-01`, UUID: "11111111-1111-1111-1111-111111111111", RemotePort: 20000},
		}},
		"duplicate remote port": {Tenants: []Tenant{
			{ServerID: "web-01", UUID: "11111111-1111-1111-1111-111111111111", RemotePort: 20000},
			{ServerID: "db-02", UUID: "22222222-2222-2222-2222-222222222222", RemotePort: 20000},
		}},
		"duplicate server id": {Tenants: []Tenant{
			{ServerID: "web-01", UUID: "11111111-1111-1111-1111-111111111111", RemotePort: 20000},
			{ServerID: "web-01", UUID: "22222222-2222-2222-2222-222222222222", RemotePort: 20001},
		}},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := RenderConfig(cfg); err == nil {
				t.Errorf("expected error for %s", name)
			}
		})
	}
}
