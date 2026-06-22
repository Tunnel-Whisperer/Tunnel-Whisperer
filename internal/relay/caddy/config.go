// Package caddy renders the relay's Caddyfile from a server list, configuring
// TLS/ACME termination and the mutual-TLS client_auth trust pool.
package caddy

import (
	"bytes"
	_ "embed"
	"fmt"
	"text/template"
)

//go:embed Caddyfile.tmpl
var caddyfileTmpl string

// Server describes one tunnel-whisperer server published on the relay. The
// fields are deliberately generic so the data model carries the forward-compat
// seams from the design spec: Role distinguishes "server" from a future
// "admin", and Upstream is a field rather than a hardcoded SSH-port assumption.
type Server struct {
	ID         string // common name; names the CA file and the route matcher
	Path       string // xhttp path, e.g. "/tw"
	CACertPath string // path to this server's CA PEM on the relay, e.g. /etc/caddy/ca/<id>.crt
	Upstream   string // reverse_proxy upstream, e.g. "h2c://127.0.0.1:10000"
	Role       string // "server" (future: "admin")
}

// Config holds everything needed to render the relay Caddyfile.
type Config struct {
	Domain           string
	Servers          []Server
	StreamCloseDelay string // e.g. "5m"
}

// RenderCaddyfile renders the relay Caddyfile: a per-site mTLS client_auth gate
// (trust pool = the union of all servers' CA PEMs) plus one path-routed handle
// block per server.
func RenderCaddyfile(cfg Config) (string, error) {
	if cfg.StreamCloseDelay == "" {
		cfg.StreamCloseDelay = "5m"
	}
	if len(cfg.Servers) == 0 {
		return "", fmt.Errorf("caddy: at least one server is required")
	}
	t, err := template.New("Caddyfile").Parse(caddyfileTmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, cfg); err != nil {
		return "", err
	}
	return buf.String(), nil
}
