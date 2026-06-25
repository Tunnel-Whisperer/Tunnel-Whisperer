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
