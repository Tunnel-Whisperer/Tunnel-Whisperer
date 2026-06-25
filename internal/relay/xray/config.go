// Package xray renders the relay's Xray config.json from a tenant list. Each
// tenant gets its own vless-in (own xhttp path + loopback port) and a routing
// destination-port allow-list ({22, RemotePort} -> freedom, else blackhole), so a
// tenant can reach only the relay sshd and its own rendezvous port — the
// transport-layer half of cross-tenant isolation.
package xray

import (
	"bytes"
	_ "embed"
	"fmt"
	"text/template"
)

//go:embed relayconfig.json.tmpl
var relayTmpl string

// Tenant is one server published on the relay.
type Tenant struct {
	ServerID   string // VLESS email + identity; equals the cert CN and path segment
	UUID       string // VLESS client id
	RemotePort int    // reverse-SSH rendezvous port (clients dial this)
}

// Config is the relay Xray render input.
type Config struct {
	Tenants []Tenant
}

type tenantView struct {
	ServerID    string
	UUID        string
	Path        string
	RemotePort  int
	VlessInPort int
}

// RenderConfig renders the relay Xray config.json.
func RenderConfig(cfg Config) (string, error) {
	if len(cfg.Tenants) == 0 {
		return "", fmt.Errorf("xray: at least one tenant is required")
	}
	views := make([]tenantView, 0, len(cfg.Tenants))
	for _, t := range cfg.Tenants {
		views = append(views, tenantView{
			ServerID:    t.ServerID,
			UUID:        t.UUID,
			Path:        "/tw/" + t.ServerID,
			RemotePort:  t.RemotePort,
			VlessInPort: t.RemotePort + 10000,
		})
	}
	tmpl, err := template.New("relayconfig").Parse(relayTmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, struct{ Tenants []tenantView }{views}); err != nil {
		return "", err
	}
	return buf.String(), nil
}
