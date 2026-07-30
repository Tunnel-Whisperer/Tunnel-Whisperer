package xray

// Single-tenant render fragments used by the gRPC live-add path (Task B3).
// The template text below must stay in sync with the corresponding fragments in
// relayconfig.json.tmpl (the per-tenant inbound block and the two routing rules).

import (
	"bytes"
	"fmt"
	"text/template"
)

// tenantInboundTmpl matches the per-tenant inbound block in relayconfig.json.tmpl
// (lines 20-33). Update both if the shape changes.
var tenantInboundTmpl = template.Must(template.New("tenant-inbound").Parse(`{
  "tag": "vless-in-{{.ServerID}}",
  "listen": "127.0.0.1",
  "port": {{.VlessInPort}},
  "protocol": "vless",
  "settings": {
    "clients": [{ "id": "{{.UUID}}", "email": "{{.ServerID}}" }],
    "decryption": "none"
  },
  "streamSettings": {
    "network": "xhttp",
    "xhttpSettings": { "path": "{{.Path}}", "mode": "stream-one" }
  }
}`))

// tenantRulesTmpl matches the two per-tenant routing rules in relayconfig.json.tmpl
// (lines 42-43). Update both if the shape changes.
var tenantRulesTmpl = template.Must(template.New("tenant-rules").Parse(`{
  "rules": [
    { "type": "field", "ruleTag": "allow-{{.ServerID}}", "inboundTag": ["vless-in-{{.ServerID}}"], "port": "22,{{.RemotePort}}", "outboundTag": "freedom" },
    { "type": "field", "ruleTag": "deny-{{.ServerID}}", "inboundTag": ["vless-in-{{.ServerID}}"], "outboundTag": "blackhole" }
  ]
}`))

// RenderTenantInbound returns the JSON object for a single vless-in-<ServerID>
// inbound, ready to hand to xray's gRPC AddInbound via infra/conf builders.
// The shape is identical to one entry emitted by RenderConfig / relayconfig.json.tmpl.
func RenderTenantInbound(t Tenant) (string, error) {
	v, err := newTenantView(t, true)
	if err != nil {
		return "", fmt.Errorf("RenderTenantInbound: %w", err)
	}
	var buf bytes.Buffer
	if err := tenantInboundTmpl.Execute(&buf, v); err != nil {
		return "", fmt.Errorf("RenderTenantInbound: %w", err)
	}
	return buf.String(), nil
}

// RenderTenantRules returns a JSON object { "rules": [allow, deny] } for one
// tenant's two routing rules, ready to hand to xray's gRPC AddRule via
// infra/conf builders. The shape is identical to the rules emitted by
// RenderConfig / relayconfig.json.tmpl for this tenant.
func RenderTenantRules(t Tenant) (string, error) {
	// UUID is not needed for rules; pass requireUUID=false.
	v, err := newTenantView(t, false)
	if err != nil {
		return "", fmt.Errorf("RenderTenantRules: %w", err)
	}
	var buf bytes.Buffer
	if err := tenantRulesTmpl.Execute(&buf, v); err != nil {
		return "", fmt.Errorf("RenderTenantRules: %w", err)
	}
	return buf.String(), nil
}
