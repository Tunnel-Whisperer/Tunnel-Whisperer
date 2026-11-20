package ops

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/egress"
)

func newEgressOps(t *testing.T, extra string) *Ops {
	t.Helper()
	return newEgressOpsEnv(t, extra, nil)
}

func newEgressOpsEnv(t *testing.T, extra string, env map[string]string) *Ops {
	t.Helper()
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
		t.Setenv(k, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, config.FilePath(), "mode: client\n"+extra)
	o, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestNewResolvesConfigProxyOverEnv(t *testing.T) {
	o := newEgressOpsEnv(t, "proxy: http://c:1\n", map[string]string{"HTTPS_PROXY": "socks5://e:2"})
	p, err := o.Proxy()
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != egress.SourceConfig || p.URLString() != "http://c:1" {
		t.Fatalf("got %s", p)
	}
}

func TestNewResolvesEnvProxy(t *testing.T) {
	o := newEgressOpsEnv(t, "", map[string]string{"ALL_PROXY": "socks5://e:2"})
	p, err := o.Proxy()
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != egress.SourceEnv || p.EnvVar != "ALL_PROXY" {
		t.Fatalf("got %s (%s)", p, p.EnvVar)
	}
}

func TestEgressErrorBlocksStart(t *testing.T) {
	o := newEgressOpsEnv(t, "", map[string]string{"HTTPS_PROXY": "ftp://x"})
	if _, err := o.Proxy(); err == nil || !strings.Contains(err.Error(), "HTTPS_PROXY") {
		t.Fatalf("Proxy() err = %v", err)
	}
	if _, err := o.egress(); err == nil {
		t.Fatal("egress() must return the stored error")
	}
	if err := o.SetProxy("http://fix:1"); err != nil {
		t.Fatal(err)
	}
	p, err := o.Proxy()
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != egress.SourceConfig || p.URLString() != "http://fix:1" {
		t.Fatalf("got %s", p)
	}
}

func TestSetProxyRejectsBadScheme(t *testing.T) {
	o := newEgressOps(t, "")
	err := o.SetProxy("ftp://x")
	if err == nil || !strings.Contains(err.Error(), "unsupported proxy scheme") {
		t.Fatalf("err = %v", err)
	}
	if got := o.Config().Proxy; got != "" {
		t.Fatalf("cfg.Proxy changed to %q", got)
	}
}

func TestReloadConfigReresolves(t *testing.T) {
	o := newEgressOps(t, "")
	writeFile(t, config.FilePath(), "mode: client\nproxy: http://later:1\n")
	if err := o.ReloadConfig(); err != nil {
		t.Fatal(err)
	}
	p, err := o.Proxy()
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != egress.SourceConfig || p.URLString() != "http://later:1" {
		t.Fatalf("got %s", p)
	}
}

func countPrefix(env []string, prefix string) int {
	n := 0
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			n++
		}
	}
	return n
}

func TestTerraformEnvCarriesConfigProxy(t *testing.T) {
	const uuid = "3f2b8c1e-0000-4000-8000-000000000001"
	o := newEgressOps(t, "xray:\n  uuid: "+uuid+"\nproxy: http://u:s@p:1\n")
	env, err := o.terraformProcessEnv(map[string]string{"X": "y"})
	if err != nil {
		t.Fatal(err)
	}
	inherited := len(os.Environ())
	tail := env[inherited:]
	if len(tail) < 4 || tail[0] != "HTTPS_PROXY=http://u:s@p:1" || tail[1] != "HTTP_PROXY=http://u:s@p:1" || tail[2] != "NO_PROXY=" || tail[3] != "no_proxy=" {
		t.Fatalf("proxy not appended after os.Environ(): %q", tail)
	}

	o2 := newEgressOps(t, "xray:\n  uuid: "+uuid+"\n")
	t.Setenv("ALL_PROXY", "socks5://e:2")
	if err := o2.ReloadConfig(); err != nil {
		t.Fatal(err)
	}
	env, err = o2.terraformProcessEnv(nil)
	if err != nil {
		t.Fatal(err)
	}
	tail = env[len(os.Environ()):]
	if len(tail) < 2 || tail[0] != "HTTPS_PROXY=socks5://e:2" || tail[1] != "HTTP_PROXY=socks5://e:2" {
		t.Fatalf("ALL_PROXY env source not translated: %q", tail)
	}

	o3 := newEgressOps(t, "xray:\n  uuid: "+uuid+"\n")
	t.Setenv("ALL_PROXY", "")
	t.Setenv("HTTPS_PROXY", "socks5://e:3")
	if err := o3.ReloadConfig(); err != nil {
		t.Fatal(err)
	}
	env, err = o3.terraformProcessEnv(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := countPrefix(env, "HTTPS_PROXY="), countPrefix(os.Environ(), "HTTPS_PROXY="); got != want {
		t.Fatalf("HTTPS_PROXY env source added entries: %d vs %d inherited", got, want)
	}
}

func TestDirectRelaySSHDialsThroughProxy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	target := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		line, _ := bufio.NewReader(c).ReadString('\n')
		target <- line
	}()

	o := newEgressOps(t, "proxy: http://"+ln.Addr().String()+"\n")
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := gossh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := gossh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(config.Dir(), "id_ed25519"), string(pem.EncodeToMemory(block)))
	writeFile(t, relayHostKeyPubPath(), string(gossh.MarshalAuthorizedKey(sshPub)))
	relayDir := config.RelayDir()
	if err := os.MkdirAll(relayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(relayDir, "terraform.tfstate"), `{"outputs":{"relay_ip":{"value":"203.0.113.9"}}}`)

	if err := o.DirectRelaySSH(func(*gossh.Client) error { return nil }); err == nil {
		t.Fatal("expected handshake failure against a closed proxy connection")
	}
	if got := <-target; !strings.HasPrefix(got, "CONNECT 203.0.113.9:22 ") {
		t.Fatalf("proxy saw %q", got)
	}
}
