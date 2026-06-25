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
