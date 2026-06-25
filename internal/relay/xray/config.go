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
	"regexp"
	"text/template"
)

//go:embed relayconfig.json.tmpl
var relayTmpl string

// serverIDRe and uuidRe gate the two values interpolated into the rendered
// JSON. They must be strictly validated to prevent JSON-string injection via a
// crafted server id or uuid.
var (
	serverIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	uuidRe     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

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

// newTenantView validates t and builds its tenantView. UUID may be empty for
// rule-only operations (RenderTenantRules does not need it).
func newTenantView(t Tenant, requireUUID bool) (tenantView, error) {
	if !serverIDRe.MatchString(t.ServerID) {
		return tenantView{}, fmt.Errorf("xray: invalid server id %q: must match %s", t.ServerID, serverIDRe)
	}
	if requireUUID && !uuidRe.MatchString(t.UUID) {
		return tenantView{}, fmt.Errorf("xray: invalid uuid %q for server %q", t.UUID, t.ServerID)
	}
	return tenantView{
		ServerID:    t.ServerID,
		UUID:        t.UUID,
		Path:        "/tw/" + t.ServerID,
		RemotePort:  t.RemotePort,
		VlessInPort: t.RemotePort + 10000,
	}, nil
}

// RenderConfig renders the relay Xray config.json.
func RenderConfig(cfg Config) (string, error) {
	if len(cfg.Tenants) == 0 {
		return "", fmt.Errorf("xray: at least one tenant is required")
	}
	seenID := make(map[string]bool, len(cfg.Tenants))
	seenPort := make(map[int]bool, len(cfg.Tenants))
	views := make([]tenantView, 0, len(cfg.Tenants))
	for _, t := range cfg.Tenants {
		if seenID[t.ServerID] {
			return "", fmt.Errorf("xray: duplicate server id %q", t.ServerID)
		}
		if seenPort[t.RemotePort] {
			return "", fmt.Errorf("xray: duplicate remote port %d", t.RemotePort)
		}
		v, err := newTenantView(t, true)
		if err != nil {
			return "", err
		}
		seenID[t.ServerID] = true
		seenPort[t.RemotePort] = true
		views = append(views, v)
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
