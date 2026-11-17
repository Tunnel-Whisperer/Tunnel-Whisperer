package xray

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderTenantInbound(t *testing.T) {
	out, err := RenderTenantInbound(Tenant{ServerID: "web-01-a1b2c3d4", UUID: "11111111-1111-1111-1111-111111111111", RemotePort: 20000})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	for _, want := range []string{`"tag": "vless-in-web-01-a1b2c3d4"`, `"port": 30000`, `"path": "/tw/web-01-a1b2c3d4"`, `"id": "11111111-1111-1111-1111-111111111111"`} {
		if !strings.Contains(out, want) {
			t.Errorf("inbound missing %q\n%s", want, out)
		}
	}
}

func TestRenderTenantRules(t *testing.T) {
	out, err := RenderTenantRules(Tenant{ServerID: "web-01-a1b2c3d4", RemotePort: 20000})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	for _, want := range []string{`"ruleTag": "allow-web-01-a1b2c3d4"`, `"port": "22,20000"`, `"ruleTag": "deny-web-01-a1b2c3d4"`, `"outboundTag": "blackhole"`} {
		if !strings.Contains(out, want) {
			t.Errorf("rules missing %q\n%s", want, out)
		}
	}
}

// TestRenderRejectsUUIDShapedServerID: the server id is the email of the
// tenant's own client; a UUID-shaped one could collide with a client the
// tenant adds (email = its uuid), which Xray refuses to load.
func TestRenderRejectsUUIDShapedServerID(t *testing.T) {
	bad := Tenant{ServerID: "dddddddd-4444-4444-8444-444444444444", UUID: "11111111-1111-1111-1111-111111111111", RemotePort: 20000}
	if _, err := RenderConfig(Config{Tenants: []Tenant{bad}}); err == nil || !strings.Contains(err.Error(), "UUID") {
		t.Errorf("RenderConfig: err = %v, want a UUID-shape refusal", err)
	}
	if _, err := RenderTenantInbound(bad); err == nil {
		t.Error("RenderTenantInbound accepted a UUID-shaped server id")
	}
	if _, err := RenderTenantRules(bad); err == nil {
		t.Error("RenderTenantRules accepted a UUID-shaped server id")
	}
}
