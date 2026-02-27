package xray

import (
	"bytes"
	_ "embed"
	"text/template"
)

//go:embed config.json.tmpl
var xrayConfigTmpl string

// Config holds the values used to render an Xray config.
type Config struct {
	UUID       string
	ListenPort int
	Domain     string
}

// RenderXrayConfig renders the embedded Xray config template with the given config.
func RenderXrayConfig(cfg Config) (string, error) {
	t, err := template.New("xray").Parse(xrayConfigTmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, cfg); err != nil {
		return "", err
	}
	return buf.String(), nil
}
