package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

func setupProxyEnv(t *testing.T, cfgProxy string, env map[string]string) {
	t.Helper()
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	for _, n := range []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(n, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	body := "mode: client\n"
	if cfgProxy != "" {
		body += "proxy: " + cfgProxy + "\n"
	}
	if err := os.WriteFile(config.FilePath(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProxyShowOutput(t *testing.T) {
	cases := []struct {
		name     string
		cfgProxy string
		env      map[string]string
		want     string
		wantErr  string
	}{
		{
			name:     "config with ignored env",
			cfgProxy: "http://user:secret@proxy:3128",
			env:      map[string]string{"HTTPS_PROXY": "socks5://other:1080"},
			want: "  Proxy: http://redacted@proxy:3128 (source: config)\n" +
				"  HTTPS_PROXY is set but ignored while a proxy is configured\n",
		},
		{
			name:     "ignored env scan order",
			cfgProxy: "http://proxy:3128",
			env:      map[string]string{"https_proxy": "socks5://a:1080", "ALL_PROXY": "socks5://b:1080"},
			want: "  Proxy: http://proxy:3128 (source: config)\n" +
				"  https_proxy is set but ignored while a proxy is configured\n",
		},
		{
			name: "env only",
			env:  map[string]string{"HTTPS_PROXY": "socks5://proxy:1080"},
			want: "  Proxy: socks5://proxy:1080 (source: env HTTPS_PROXY)\n" +
				"  (resolved from this shell's environment; a service may differ — check the daemon log line 'outbound proxy')\n",
		},
		{
			name: "none",
			want: "  Proxy: none\n",
		},
		{
			name:    "invalid env",
			env:     map[string]string{"HTTPS_PROXY": "ftp://proxy:21"},
			wantErr: "HTTPS_PROXY",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setupProxyEnv(t, c.cfgProxy, c.env)
			var err error
			out := captureStdout(t, func() { err = runProxyShow(nil, nil) })
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if out != c.want {
				t.Errorf("output = %q, want %q", out, c.want)
			}
			if strings.Contains(out, "secret") {
				t.Errorf("credentials leaked:\n%s", out)
			}
		})
	}
}

func TestProxySetRedactsCredentials(t *testing.T) {
	setupProxyEnv(t, "", nil)
	var err error
	out := captureStdout(t, func() { err = runProxySet(nil, []string{"http://u:secret@p:8080"}) })
	if err != nil {
		t.Fatal(err)
	}
	if first := strings.SplitN(out, "\n", 2)[0]; first != "  Proxy set to: http://redacted@p:8080" {
		t.Errorf("first line = %q", first)
	}
	if strings.Contains(out, "secret") {
		t.Errorf("credentials leaked:\n%s", out)
	}
	raw, err := os.ReadFile(config.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "http://u:secret@p:8080") {
		t.Errorf("config does not hold the raw URL:\n%s", raw)
	}
}
