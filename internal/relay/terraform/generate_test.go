package terraform

import (
	"strings"
	"testing"
)

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
