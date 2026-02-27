package caddy

import (
	"bytes"
	_ "embed"
	"text/template"
)

//go:embed Caddyfile.tmpl
var caddyfileTmpl string

// Config holds the values used to render a Caddyfile.
type Config struct {
	Domain           string
	OAuthUpstream    string // e.g. "localhost:9000"
	ProtectedUpstream string // e.g. "localhost:2222"
}

// RenderCaddyfile renders the embedded Caddyfile template with the given config.
func RenderCaddyfile(cfg Config) (string, error) {
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
