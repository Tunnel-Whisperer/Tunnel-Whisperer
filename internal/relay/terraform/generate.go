package terraform

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
)

//go:embed cloud-init.yaml.tmpl
var cloudInitTmpl string

//go:embed aws.tf.tmpl
var awsTfTmpl string

//go:embed hetzner.tf.tmpl
var hetznerTfTmpl string

//go:embed digitalocean.tf.tmpl
var digitaloceanTfTmpl string

//go:embed install-script.sh.tmpl
var installScriptTmpl string

// Pinned versions — keeps relay setup reproducible even when upstream releases new versions.
const (
	XrayVersion = "v1.8.24" // must stay compatible with the xray-core in go.mod
)

// Config holds all values needed to render relay files.
type Config struct {
	Domain        string
	UUID          string
	XrayPath      string
	SSHUser       string
	PublicKey     string
	Provider      string // "aws", "hetzner", or "digitalocean"
	CaddyCertsB64 string // base64-encoded tar.gz of saved Caddy TLS certs (optional)
	XrayVersion   string // populated automatically from the pinned constant
}

var providerTemplates = map[string]string{
	"aws":          awsTfTmpl,
	"hetzner":      hetznerTfTmpl,
	"digitalocean": digitaloceanTfTmpl,
}

// Generate renders cloud-init.yaml and the selected provider's main.tf into dir.
func Generate(dir string, cfg Config) error {
	cfg.XrayVersion = XrayVersion
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating relay directory: %w", err)
	}

	// cloud-init.yaml — universal across all providers.
	content, err := render("cloud-init.yaml", cloudInitTmpl, cfg)
	if err != nil {
		return fmt.Errorf("rendering cloud-init.yaml: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cloud-init.yaml"), []byte(content), 0644); err != nil {
		return fmt.Errorf("writing cloud-init.yaml: %w", err)
	}

	// main.tf — only the selected provider.
	tmpl, ok := providerTemplates[cfg.Provider]
	if !ok {
		return fmt.Errorf("unknown provider: %s", cfg.Provider)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(tmpl), 0644); err != nil {
		return fmt.Errorf("writing main.tf: %w", err)
	}

	return nil
}

// GenerateInstallScript renders the manual install bash script with the given config.
func GenerateInstallScript(cfg Config) (string, error) {
	cfg.XrayVersion = XrayVersion
	return render("install-script.sh", installScriptTmpl, cfg)
}

func render(name, tmplStr string, cfg Config) (string, error) {
	t, err := template.New(name).Parse(tmplStr)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, cfg); err != nil {
		return "", err
	}
	return buf.String(), nil
}
