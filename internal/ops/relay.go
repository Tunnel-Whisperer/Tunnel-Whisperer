package ops

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/relay/terraform"
	gossh "golang.org/x/crypto/ssh"
)

// RelayProvisionRequest contains everything needed to provision a relay.
type RelayProvisionRequest struct {
	Domain       string `json:"domain"`
	ProviderKey  string `json:"provider_key"`  // "hetzner", "digitalocean", "aws"
	ProviderName string `json:"provider_name"` // display name
	Token        string `json:"token"`
	AWSSecretKey string `json:"aws_secret_key"`
	Region       string `json:"region"` // provider region/location
}

// RelayStatus describes the current state of the relay.
type RelayStatus struct {
	Provisioned bool   `json:"provisioned"`
	Domain      string `json:"domain"`
	IP          string `json:"ip,omitempty"`
	Provider    string `json:"provider,omitempty"`
}

// ManualRelayMarker is written to the relay directory when the user sets up
// a relay manually (without Terraform).
type ManualRelayMarker struct {
	Domain    string `json:"domain"`
	IP        string `json:"ip"`
	CreatedAt string `json:"created_at"`
}

// GetRelayStatus checks if a relay has been provisioned.
func (o *Ops) GetRelayStatus() RelayStatus {
	cfg := o.Config()
	relayDir := config.RelayDir()

	status := RelayStatus{
		Domain: cfg.Xray.RelayHost,
	}

	// Check for tfstate to determine if cloud-provisioned.
	if _, err := os.Stat(filepath.Join(relayDir, "terraform.tfstate")); err == nil {
		status.Provisioned = true
		// Try to read the IP from terraform output.
		ip, err := o.TerraformOutput(relayDir, nil, "relay_ip")
		if err == nil {
			status.IP = ip
		}
		// Detect provider from main.tf.
		if data, err := os.ReadFile(filepath.Join(relayDir, "main.tf")); err == nil {
			tf := string(data)
			switch {
			case strings.Contains(tf, `provider "hcloud"`):
				status.Provider = "Hetzner"
			case strings.Contains(tf, `provider "digitalocean"`):
				status.Provider = "DigitalOcean"
			case strings.Contains(tf, `provider "aws"`):
				status.Provider = "AWS"
			}
		}
		return status
	}

	// Check for manual relay marker.
	if data, err := os.ReadFile(filepath.Join(relayDir, "manual-relay.json")); err == nil {
		var marker ManualRelayMarker
		if json.Unmarshal(data, &marker) == nil {
			status.Provisioned = true
			status.IP = marker.IP
			status.Provider = "Manual"
		}
	}

	return status
}

// ProvisionRelay runs the full 9-step relay provisioning flow.
// Progress events are sent through the callback. This method blocks until
// the relay is provisioned or the context is cancelled.
func (o *Ops) ProvisionRelay(ctx context.Context, req RelayProvisionRequest, progress ProgressFunc) error {
	if progress == nil {
		progress = func(ProgressEvent) {}
	}

	relayDir := config.RelayDir()

	// Step 1: SSH keys.
	progress(ProgressEvent{Step: 1, Total: 9, Label: "SSH keys", Status: "running"})
	if err := o.EnsureKeys(); err != nil {
		progress(ProgressEvent{Step: 1, Total: 9, Label: "SSH keys", Status: "failed", Error: err.Error()})
		return err
	}
	progress(ProgressEvent{Step: 1, Total: 9, Label: "SSH keys", Status: "completed"})

	// Step 2: Xray UUID.
	progress(ProgressEvent{Step: 2, Total: 9, Label: "Xray UUID", Status: "running"})
	o.mu.Lock()
	cfg := o.cfg
	if cfg.Xray.UUID == "" {
		cfg.Xray.UUID = uuid.New().String()
		if err := config.Save(cfg); err != nil {
			o.mu.Unlock()
			progress(ProgressEvent{Step: 2, Total: 9, Label: "Xray UUID", Status: "failed", Error: err.Error()})
			return fmt.Errorf("saving config: %w", err)
		}
	}
	o.mu.Unlock()
	progress(ProgressEvent{Step: 2, Total: 9, Label: "Xray UUID", Status: "completed", Message: cfg.Xray.UUID})

	// Step 3: Domain.
	progress(ProgressEvent{Step: 3, Total: 9, Label: "Relay domain", Status: "running"})
	o.mu.Lock()
	if req.Domain != "" {
		cfg.Xray.RelayHost = req.Domain
		if err := config.Save(cfg); err != nil {
			o.mu.Unlock()
			progress(ProgressEvent{Step: 3, Total: 9, Label: "Relay domain", Status: "failed", Error: err.Error()})
			return fmt.Errorf("saving config: %w", err)
		}
	}
	relayHost := cfg.Xray.RelayHost
	o.mu.Unlock()
	if relayHost == "" {
		progress(ProgressEvent{Step: 3, Total: 9, Label: "Relay domain", Status: "failed", Error: "domain is required"})
		return fmt.Errorf("relay domain is required")
	}
	progress(ProgressEvent{Step: 3, Total: 9, Label: "Relay domain", Status: "completed", Message: relayHost})

	// Step 4: Cloud provider (already selected via req).
	progress(ProgressEvent{Step: 4, Total: 9, Label: "Cloud provider", Status: "completed", Message: req.ProviderName})

	// Step 5: Credentials (already provided via req).
	progress(ProgressEvent{Step: 5, Total: 9, Label: "Credentials", Status: "running"})
	if err := o.TestCloudCredentials(req.ProviderName, req.Token, req.AWSSecretKey); err != nil {
		progress(ProgressEvent{Step: 5, Total: 9, Label: "Credentials", Status: "failed", Error: err.Error()})
		return fmt.Errorf("credential test failed: %w", err)
	}
	progress(ProgressEvent{Step: 5, Total: 9, Label: "Credentials", Status: "completed"})

	// Step 6: Not used in dashboard flow (confirmation is done by the frontend).
	progress(ProgressEvent{Step: 6, Total: 9, Label: "Confirmation", Status: "completed"})

	// Step 7: Terraform provisioning.
	progress(ProgressEvent{Step: 7, Total: 9, Label: "Provisioning", Status: "running", Message: "Generating Terraform files"})

	pubKeyPath := filepath.Join(config.Dir(), "id_ed25519.pub")
	pubKeyBytes, err := os.ReadFile(pubKeyPath)
	if err != nil {
		progress(ProgressEvent{Step: 7, Total: 9, Label: "Provisioning", Status: "failed", Error: err.Error()})
		return fmt.Errorf("reading public key: %w", err)
	}

	tfCfg := terraform.Config{
		Domain:    cfg.Xray.RelayHost,
		UUID:      cfg.Xray.UUID,
		XrayPath:  cfg.Xray.Path,
		SSHUser:   cfg.Server.RelaySSHUser,
		PublicKey: strings.TrimSpace(string(pubKeyBytes)),
		Provider:  req.ProviderKey,
	}

	// Load saved TLS certificates for reuse (avoids Let's Encrypt rate limits).
	if certData, err := os.ReadFile(caddyCertsPath(cfg.Xray.RelayHost)); err == nil {
		tfCfg.CaddyCertsB64 = base64.StdEncoding.EncodeToString(certData)
		slog.Info("reusing saved TLS certificates", "domain", cfg.Xray.RelayHost)
	}

	if err := terraform.Generate(relayDir, tfCfg); err != nil {
		progress(ProgressEvent{Step: 7, Total: 9, Label: "Provisioning", Status: "failed", Error: err.Error()})
		return fmt.Errorf("generating terraform files: %w", err)
	}

	// Write credentials and region.
	tfEnv := map[string]string{}
	var tfvars string
	if req.ProviderName == "AWS" {
		tfEnv["AWS_ACCESS_KEY_ID"] = req.Token
		tfEnv["AWS_SECRET_ACCESS_KEY"] = req.AWSSecretKey
	} else {
		for _, p := range CloudProviders() {
			if p.Key == req.ProviderKey {
				tfvars += fmt.Sprintf("%s = %q\n", p.VarName, req.Token)
				break
			}
		}
	}
	if req.Region != "" {
		regionVar := "region"
		if req.ProviderKey == "hetzner" {
			regionVar = "location"
		}
		tfvars += fmt.Sprintf("%s = %q\n", regionVar, req.Region)
	}
	if tfvars != "" {
		tfvarsPath := filepath.Join(relayDir, "terraform.tfvars")
		if err := os.WriteFile(tfvarsPath, []byte(tfvars), 0600); err != nil {
			progress(ProgressEvent{Step: 7, Total: 9, Label: "Provisioning", Status: "failed", Error: err.Error()})
			return fmt.Errorf("writing terraform.tfvars: %w", err)
		}
	}

	progress(ProgressEvent{Step: 7, Total: 9, Label: "Provisioning", Status: "running", Message: "terraform init"})
	if err := o.RunTerraform(ctx, relayDir, tfEnv, progress, "init"); err != nil {
		progress(ProgressEvent{Step: 7, Total: 9, Label: "Provisioning", Status: "failed", Error: err.Error()})
		return err
	}

	progress(ProgressEvent{Step: 7, Total: 9, Label: "Provisioning", Status: "running", Message: "terraform apply"})
	if err := o.RunTerraform(ctx, relayDir, tfEnv, progress, "apply", "-auto-approve"); err != nil {
		progress(ProgressEvent{Step: 7, Total: 9, Label: "Provisioning", Status: "failed", Error: err.Error()})
		return err
	}

	relayIP, err := o.TerraformOutput(relayDir, tfEnv, "relay_ip")
	if err != nil {
		progress(ProgressEvent{Step: 7, Total: 9, Label: "Provisioning", Status: "failed", Error: err.Error()})
		return fmt.Errorf("could not read relay IP: %w", err)
	}
	progress(ProgressEvent{Step: 7, Total: 9, Label: "Provisioning", Status: "completed", Message: "Relay IP: " + relayIP, Data: relayIP})

	// Step 8: DNS & readiness.
	progress(ProgressEvent{Step: 8, Total: 9, Label: "DNS & readiness", Status: "running",
		Message: fmt.Sprintf("Set DNS A record: %s → %s", cfg.Xray.RelayHost, relayIP)})

	if err := o.WaitForDNS(ctx, cfg.Xray.RelayHost, relayIP, progress); err != nil {
		slog.Warn("DNS wait cancelled", "error", err)
		progress(ProgressEvent{Step: 8, Total: 9, Label: "DNS & readiness", Status: "completed",
			Message: "DNS not verified — set your A record and run Test Connectivity from the relay page"})
	} else {
		// DNS resolved — now wait for HTTPS (Caddy + TLS cert).
		progress(ProgressEvent{Step: 8, Total: 9, Label: "DNS & readiness", Status: "running",
			Message: "DNS verified — waiting for Caddy to obtain TLS certificate..."})

		if err := o.WaitForRelay(ctx, cfg.Xray.RelayHost, 5*time.Minute, progress); err != nil {
			slog.Warn("relay readiness timed out", "error", err)
			progress(ProgressEvent{Step: 8, Total: 9, Label: "DNS & readiness", Status: "completed",
				Message: "TLS not ready yet — Caddy will keep retrying. Check relay page in a few minutes."})
		} else {
			progress(ProgressEvent{Step: 8, Total: 9, Label: "DNS & readiness", Status: "completed",
				Message: "Relay is live — DNS resolved and TLS certificate obtained"})
		}
	}

	// Step 9: Cloud-init log (best-effort).
	progress(ProgressEvent{Step: 9, Total: 9, Label: "Cloud-init log", Status: "running", Message: "Reading cloud-init output from relay..."})
	o.ReadCloudInitLog(cfg, progress)
	progress(ProgressEvent{Step: 9, Total: 9, Label: "Cloud-init log", Status: "completed"})

	return nil
}

// GenerateManualInstallScript prepares SSH keys, UUID, and config, then
// returns a bash script for manual relay installation.
func (o *Ops) GenerateManualInstallScript(domain string) (string, error) {
	if err := o.EnsureKeys(); err != nil {
		return "", fmt.Errorf("ensuring keys: %w", err)
	}

	o.mu.Lock()
	cfg := o.cfg
	if cfg.Xray.UUID == "" {
		cfg.Xray.UUID = uuid.New().String()
		if err := config.Save(cfg); err != nil {
			o.mu.Unlock()
			return "", fmt.Errorf("saving config: %w", err)
		}
	}
	if domain != "" {
		cfg.Xray.RelayHost = domain
		if err := config.Save(cfg); err != nil {
			o.mu.Unlock()
			return "", fmt.Errorf("saving config: %w", err)
		}
	}
	o.mu.Unlock()

	pubKeyPath := filepath.Join(config.Dir(), "id_ed25519.pub")
	pubKeyBytes, err := os.ReadFile(pubKeyPath)
	if err != nil {
		return "", fmt.Errorf("reading public key: %w", err)
	}

	tfCfg := terraform.Config{
		Domain:    cfg.Xray.RelayHost,
		UUID:      cfg.Xray.UUID,
		XrayPath:  cfg.Xray.Path,
		SSHUser:   cfg.Server.RelaySSHUser,
		PublicKey: strings.TrimSpace(string(pubKeyBytes)),
	}

	return terraform.GenerateInstallScript(tfCfg)
}

// SaveManualRelay writes the manual relay marker file, marking the relay as provisioned.
func (o *Ops) SaveManualRelay(domain, ip string) error {
	relayDir := config.RelayDir()
	if err := os.MkdirAll(relayDir, 0755); err != nil {
		return fmt.Errorf("creating relay directory: %w", err)
	}

	marker := ManualRelayMarker{
		Domain:    domain,
		IP:        ip,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(relayDir, "manual-relay.json"), data, 0644)
}

// caddyCertsPath returns the local path for a domain's archived Caddy TLS
// certificate data: <config>/archive/<domain>/caddy-certs.tar.gz
func caddyCertsPath(domain string) string {
	return filepath.Join(config.Dir(), "archive", domain, "caddy-certs.tar.gz")
}

// saveCaddyCerts SSHes into the relay and saves the Caddy TLS data directory
// as a local tarball. This is best-effort with a 30-second timeout: errors
// are logged but do not block the caller.
func (o *Ops) saveCaddyCerts(ctx context.Context, progress ProgressFunc) {
	cfg := o.Config()
	domain := cfg.Xray.RelayHost
	if domain == "" {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- withRelaySSH(cfg, func(client *gossh.Client) error {
			session, err := client.NewSession()
			if err != nil {
				return err
			}
			defer session.Close()

			out, err := session.Output("sudo tar czf - -C /var/lib/caddy/.local/share/caddy . 2>/dev/null")
			if err != nil {
				return fmt.Errorf("tarring caddy data: %w", err)
			}
			if len(out) == 0 {
				return fmt.Errorf("no certificate data found")
			}

			certPath := caddyCertsPath(domain)
			if err := os.MkdirAll(filepath.Dir(certPath), 0700); err != nil {
				return fmt.Errorf("creating archive directory: %w", err)
			}
			return os.WriteFile(certPath, out, 0600)
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			slog.Warn("could not save TLS certificates", "error", err)
			progress(ProgressEvent{Message: "Could not save TLS certificates (non-fatal): " + err.Error()})
		} else {
			slog.Info("TLS certificates saved", "domain", domain)
			progress(ProgressEvent{Message: "TLS certificates saved for reuse"})
		}
	case <-ctx.Done():
		slog.Warn("cert saving timed out or cancelled")
		progress(ProgressEvent{Message: "TLS certificate saving skipped (timeout/cancelled)"})
	}
}

// DestroyRelay tears down the relay infrastructure.
func (o *Ops) DestroyRelay(ctx context.Context, creds map[string]string, progress ProgressFunc) error {
	if progress == nil {
		progress = func(ProgressEvent) {}
	}

	relayDir := config.RelayDir()

	// Manual relay: just remove the marker and clean up.
	if _, err := os.Stat(filepath.Join(relayDir, "manual-relay.json")); err == nil {
		progress(ProgressEvent{Step: 1, Total: 2, Label: "Removing manual relay", Status: "running"})
		if err := os.RemoveAll(relayDir); err != nil {
			progress(ProgressEvent{Step: 1, Total: 2, Label: "Removing manual relay", Status: "failed", Error: err.Error()})
			return fmt.Errorf("removing relay directory: %w", err)
		}
		progress(ProgressEvent{Step: 1, Total: 2, Label: "Removing manual relay", Status: "completed"})

		progress(ProgressEvent{Step: 2, Total: 2, Label: "Cleaning up", Status: "running"})
		deactivateAllUsers()
		progress(ProgressEvent{Step: 2, Total: 2, Label: "Cleaning up", Status: "completed"})
		return nil
	}

	if _, err := os.Stat(filepath.Join(relayDir, "terraform.tfstate")); os.IsNotExist(err) {
		return fmt.Errorf("no relay to destroy (no tfstate or manual marker found)")
	}

	// Step 1: Save TLS certificates for reuse (best-effort).
	progress(ProgressEvent{Step: 1, Total: 3, Label: "Saving TLS certificates", Status: "running"})
	o.saveCaddyCerts(ctx, progress)
	progress(ProgressEvent{Step: 1, Total: 3, Label: "Saving TLS certificates", Status: "completed"})

	// Step 2: Terraform destroy.
	progress(ProgressEvent{Step: 2, Total: 3, Label: "Destroying relay", Status: "running"})
	if err := o.RunTerraform(ctx, relayDir, creds, progress, "destroy", "-auto-approve"); err != nil {
		progress(ProgressEvent{Step: 2, Total: 3, Label: "Destroying relay", Status: "failed", Error: err.Error()})
		return err
	}
	progress(ProgressEvent{Step: 2, Total: 3, Label: "Destroying relay", Status: "completed"})

	// Step 3: Clean up.
	progress(ProgressEvent{Step: 3, Total: 3, Label: "Cleaning up", Status: "running"})
	if err := os.RemoveAll(relayDir); err != nil {
		progress(ProgressEvent{Step: 3, Total: 3, Label: "Cleaning up", Status: "failed", Error: err.Error()})
		return fmt.Errorf("removing relay directory: %w", err)
	}

	// Deactivate all users — their UUIDs are no longer on any relay.
	deactivateAllUsers()

	progress(ProgressEvent{Step: 3, Total: 3, Label: "Cleaning up", Status: "completed"})

	return nil
}

// TestRelay runs connectivity checks against the relay, streaming each
// result as a progress event so the dashboard shows real-time feedback.
func (o *Ops) TestRelay(progress ProgressFunc) {
	if progress == nil {
		progress = func(ProgressEvent) {}
	}

	cfg := o.Config()
	domain := cfg.Xray.RelayHost

	// 1. DNS resolution.
	progress(ProgressEvent{Step: 1, Total: 3, Label: "DNS", Status: "running"})
	addrs, err := net.LookupHost(domain)
	if err != nil {
		progress(ProgressEvent{Step: 1, Total: 3, Label: "DNS", Status: "failed", Error: err.Error()})
		return
	}
	progress(ProgressEvent{Step: 1, Total: 3, Label: "DNS", Status: "completed", Message: strings.Join(addrs, ", ")})

	// 2. HTTPS (Caddy).
	progress(ProgressEvent{Step: 2, Total: 3, Label: "HTTPS (Caddy)", Status: "running"})
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Get("https://" + domain)
	if err != nil {
		progress(ProgressEvent{Step: 2, Total: 3, Label: "HTTPS (Caddy)", Status: "failed", Error: err.Error()})
		return
	}
	resp.Body.Close()
	progress(ProgressEvent{Step: 2, Total: 3, Label: "HTTPS (Caddy)", Status: "completed", Message: fmt.Sprintf("HTTP %d", resp.StatusCode)})

	// 3. Xray + SSH through tunnel.
	progress(ProgressEvent{Step: 3, Total: 3, Label: "Xray + SSH", Status: "running"})
	err = withRelaySSH(cfg, func(client *gossh.Client) error {
		session, err := client.NewSession()
		if err != nil {
			return err
		}
		defer session.Close()
		_, err = session.Output("echo ok")
		return err
	})
	if err != nil {
		progress(ProgressEvent{Step: 3, Total: 3, Label: "Xray + SSH", Status: "failed", Error: err.Error()})
	} else {
		progress(ProgressEvent{Step: 3, Total: 3, Label: "Xray + SSH", Status: "completed", Message: "tunnel and shell working"})
	}
}

// RelaySSH opens a temporary Xray tunnel to the relay and passes the SSH
// client to fn. The tunnel is torn down when fn returns.
func (o *Ops) RelaySSH(fn func(client *gossh.Client) error) error {
	cfg := o.Config()
	return withRelaySSH(cfg, fn)
}

// ReadCloudInitLog connects to the relay via the Xray tunnel and reads
// /var/log/cloud-init-output.log, streaming each line as a progress event.
// This is best-effort: errors are reported as progress messages but do not
// cause provisioning to fail.
func (o *Ops) ReadCloudInitLog(cfg *config.Config, progress ProgressFunc) {
	err := withRelaySSH(cfg, func(client *gossh.Client) error {
		session, err := client.NewSession()
		if err != nil {
			return err
		}
		defer session.Close()

		out, err := session.Output("sudo cat /var/log/cloud-init-output.log 2>/dev/null")
		if err != nil {
			return err
		}

		for _, line := range strings.Split(string(out), "\n") {
			if line != "" {
				progress(ProgressEvent{Message: line})
			}
		}
		return nil
	})
	if err != nil {
		slog.Warn("could not read cloud-init log", "error", err)
		progress(ProgressEvent{Message: fmt.Sprintf("Could not read cloud-init log: %v", err)})
	}
}

// WaitForDNS polls DNS every 5 seconds until the domain resolves to the
// expected IP. Progress updates in-place on a single line (step 8) showing
// attempt count, elapsed time, and last result.
// The loop runs indefinitely until DNS matches or the context is cancelled.
func (o *Ops) WaitForDNS(ctx context.Context, domain, expectedIP string, progress ProgressFunc) error {
	if progress == nil {
		progress = func(ProgressEvent) {}
	}

	// Emit a dns_setup event so the frontend can show the instruction card.
	progress(ProgressEvent{Step: 8, Total: 9, Label: "DNS", Status: "running",
		Message: fmt.Sprintf("Set A record: %s → %s", domain, expectedIP),
		Data:    "dns_setup:" + domain + ":" + expectedIP})

	start := time.Now()
	attempt := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		attempt++
		elapsed := time.Since(start).Truncate(time.Second)

		addrs, err := net.LookupHost(domain)
		if err != nil {
			progress(ProgressEvent{Step: 8, Total: 9, Label: "DNS", Status: "running",
				Message: fmt.Sprintf("attempt #%d (%s) — not resolving", attempt, elapsed)})
		} else if len(addrs) == 0 {
			progress(ProgressEvent{Step: 8, Total: 9, Label: "DNS", Status: "running",
				Message: fmt.Sprintf("attempt #%d (%s) — no records", attempt, elapsed)})
		} else {
			for _, addr := range addrs {
				if addr == expectedIP {
					return nil
				}
			}
			progress(ProgressEvent{Step: 8, Total: 9, Label: "DNS", Status: "running",
				Message: fmt.Sprintf("attempt #%d (%s) — resolves to %s, waiting for %s", attempt, elapsed, strings.Join(addrs, ", "), expectedIP)})
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// WaitForRelay polls the relay until HTTPS responds. Progress updates
// in-place on a single line (step 8) showing attempt count, elapsed time,
// and a human-readable reason for the current failure.
func (o *Ops) WaitForRelay(ctx context.Context, domain string, timeout time.Duration, progress ProgressFunc) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 10 * time.Second}
	start := time.Now()
	attempt := 0

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		attempt++
		elapsed := time.Since(start).Truncate(time.Second)

		resp, err := client.Get("https://" + domain)
		if err == nil {
			resp.Body.Close()
			return nil
		}

		errStr := err.Error()
		var reason string
		switch {
		case strings.Contains(errStr, "no such host"):
			reason = "DNS not resolving yet"
		case strings.Contains(errStr, "connection refused"):
			reason = "Caddy not listening yet"
		case strings.Contains(errStr, "tls:") || strings.Contains(errStr, "certificate"):
			reason = "TLS cert not ready (Let's Encrypt in progress)"
		case strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline"):
			reason = "connection timed out (server booting)"
		default:
			reason = errStr
		}

		if progress != nil {
			progress(ProgressEvent{Step: 8, Total: 9, Label: "TLS", Status: "running",
				Message: fmt.Sprintf("attempt #%d (%s) — %s", attempt, elapsed, reason)})
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
	return fmt.Errorf("timeout after %s: %s not responding to HTTPS", timeout, domain)
}
