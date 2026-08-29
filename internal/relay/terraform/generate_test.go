package terraform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSSHOpenKeyPin: the tw key line in the relay's authorized_keys is
// pinned from="127.0.0.1" (tunnel-only) UNLESS the relay is provisioned
// with --ssh-open — an open port 22 is useless if the key that should use
// it only authenticates from loopback. Applies to both the manual install
// script and the cloud-init used by the Terraform providers.
func TestSSHOpenKeyPin(t *testing.T) {
	base := Config{
		Domain: "relay.example", UUID: "u-1", XrayPath: "/tw/x", SSHUser: "tw",
		PublicKey: "ssh-ed25519 AAAA admin@tw", ServerID: "adm-1", Provider: "hetzner",
		CACertB64: "Zm9v", CaddyfileB64: "Zm9v", XrayConfigB64: "Zm9v",
	}

	closed, err := GenerateInstallScript(base)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(closed, `from=\"127.0.0.1\" $TW_PUBKEY`) {
		t.Error("closed install script must pin the tw key to loopback")
	}

	open := base
	open.SSHOpen = true
	script, err := GenerateInstallScript(open)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(script, `from=\"127.0.0.1\" $TW_PUBKEY`) {
		t.Error("ssh-open install script must NOT pin the tw key to loopback")
	}
	if !strings.Contains(script, `echo "$TW_PUBKEY" >> "$AK"`) {
		t.Errorf("ssh-open install script must still append the tw key:\n%s", script)
	}

	for name, cfg := range map[string]Config{"closed": base, "open": open} {
		dir := t.TempDir()
		if err := Generate(dir, cfg); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "cloud-init.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		pinned := strings.Contains(string(data), `from="127.0.0.1" ssh-ed25519 AAAA admin@tw`)
		if name == "closed" && !pinned {
			t.Error("closed cloud-init must pin the tw key to loopback")
		}
		if name == "open" && pinned {
			t.Error("ssh-open cloud-init must NOT pin the tw key to loopback")
		}
		if !strings.Contains(string(data), "ssh-ed25519 AAAA admin@tw") {
			t.Errorf("%s cloud-init lost the tw key entirely", name)
		}
	}
}

// TestXrayInstallNotPipedToShell is the SP-6 regression: neither provisioning
// path may pipe a remote script straight into a root shell. Both must download
// the installer to a file with `curl -fsSL` and run `bash <file>`.
func TestXrayInstallNotPipedToShell(t *testing.T) {
	cfg := Config{
		Domain: "relay.example", UUID: "u-1", XrayPath: "/tw/x", SSHUser: "tw",
		PublicKey: "ssh-ed25519 AAAA admin@tw", ServerID: "adm-1", Provider: "hetzner",
		CACertB64: "Zm9v", CaddyfileB64: "Zm9v", XrayConfigB64: "Zm9v",
	}
	script, err := GenerateInstallScript(cfg)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := Generate(dir, cfg); err != nil {
		t.Fatal(err)
	}
	ci, err := os.ReadFile(filepath.Join(dir, "cloud-init.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{"install-script": script, "cloud-init": string(ci)} {
		if strings.Contains(out, `bash -c "$(curl`) {
			t.Errorf("%s pipes curl output straight into bash (SP-6)", name)
		}
		if !strings.Contains(out, "curl -fsSL") {
			t.Errorf("%s must download the Xray installer with curl -fsSL", name)
		}
	}
}

// TestRelayHostKeyInjected is the SP-7 regression: when a host key is supplied,
// both paths install it at /etc/ssh/ssh_host_ed25519_key so the admin can pin
// it; when it is absent (a legacy config) neither path touches the host key.
func TestRelayHostKeyInjected(t *testing.T) {
	base := Config{
		Domain: "relay.example", UUID: "u-1", XrayPath: "/tw/x", SSHUser: "tw",
		PublicKey: "ssh-ed25519 AAAA admin@tw", ServerID: "adm-1", Provider: "hetzner",
		CACertB64: "Zm9v", CaddyfileB64: "Zm9v", XrayConfigB64: "Zm9v",
	}
	withKey := base
	withKey.HostKeyB64 = "aG9zdGtleQ==" // "hostkey"
	withKey.HostKeyPub = "ssh-ed25519 AAAAhostpub relay@tw"

	render := func(c Config) (string, string) {
		script, err := GenerateInstallScript(c)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		if err := Generate(dir, c); err != nil {
			t.Fatal(err)
		}
		ci, err := os.ReadFile(filepath.Join(dir, "cloud-init.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		return script, string(ci)
	}

	script, ci := render(withKey)
	for name, out := range map[string]string{"install-script": script, "cloud-init": ci} {
		if !strings.Contains(out, "/etc/ssh/ssh_host_ed25519_key") {
			t.Errorf("%s must install the tw host key when supplied:\n%s", name, out)
		}
		if !strings.Contains(out, "aG9zdGtleQ==") {
			t.Errorf("%s missing the host key material", name)
		}
	}

	script, ci = render(base) // no host key
	for name, out := range map[string]string{"install-script": script, "cloud-init": ci} {
		if strings.Contains(out, "ssh_host_ed25519_key") {
			t.Errorf("%s must not touch the host key when none is supplied", name)
		}
	}
}

// TestInstallScriptRerunSafe: re-running the install script on the same VM
// must not abort under `set -e`. The known trap is the Caddy repo setup —
// `gpg --dearmor -o <keyring>` refuses to overwrite an existing file, so a
// re-run died at step 3. The cleanup block must remove the keyring and the
// sources list, and gpg must carry --yes as a second line of defense.
func TestInstallScriptRerunSafe(t *testing.T) {
	script, err := GenerateInstallScript(Config{
		Domain: "relay.example", UUID: "u-1", XrayPath: "/tw/x", SSHUser: "tw",
		PublicKey: "ssh-ed25519 AAAA admin@tw", ServerID: "adm-1",
		CACertB64: "Zm9v", CaddyfileB64: "Zm9v", XrayConfigB64: "Zm9v",
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanup := strings.Index(script, "Cleaning previous installation")
	dearmor := strings.Index(script, "--dearmor -o")
	if cleanup < 0 || dearmor < 0 {
		t.Fatalf("script missing cleanup block or gpg --dearmor:\n%s", script)
	}
	rmKeyring := strings.Index(script, "rm -f /usr/share/keyrings/caddy-stable-archive-keyring.gpg")
	if rmKeyring < 0 {
		t.Error("cleanup must remove the stale caddy keyring (gpg --dearmor aborts on an existing file)")
	} else if !(cleanup < rmKeyring && rmKeyring < dearmor) {
		t.Error("keyring removal must sit in the cleanup block, before gpg --dearmor runs")
	}
	if !strings.Contains(script, "/etc/apt/sources.list.d/caddy-stable.list") ||
		!strings.Contains(script[cleanup:dearmor], "caddy-stable.list") {
		t.Error("cleanup must also remove the stale caddy sources list")
	}
	if !strings.Contains(script, "gpg --yes --dearmor") {
		t.Error("gpg --dearmor must carry --yes so an existing keyring never aborts the run")
	}
}
