package caddy

import (
	"strings"
	"testing"
)

func TestRenderCaddyfileSingleServer(t *testing.T) {
	out, err := RenderCaddyfile(Config{
		Domain: "relay.example.com",
		Servers: []Server{{
			ID:         "tw-server",
			Path:       "/tw",
			CACertPath: "/etc/caddy/ca/tw-server.crt",
			Upstream:   "h2c://127.0.0.1:10000",
			Role:       "server",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"relay.example.com {",
		"mode require_and_verify",
		"trust_pool file /etc/caddy/ca/tw-server.crt",
		"protocols tls1.3",
		"@tw-server path /tw*",
		"reverse_proxy h2c://127.0.0.1:10000",
		"stream_close_delay 5m",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered Caddyfile missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderCaddyfileRequiresServer(t *testing.T) {
	if _, err := RenderCaddyfile(Config{Domain: "x"}); err == nil {
		t.Error("expected error when no servers provided")
	}
}

func TestRenderCaddyfileMultiServerTrustPool(t *testing.T) {
	out, err := RenderCaddyfile(Config{
		Domain: "relay.example.com",
		Servers: []Server{
			{ID: "a", Path: "/a", CACertPath: "/etc/caddy/ca/a.crt", Upstream: "h2c://127.0.0.1:10000", Role: "server"},
			{ID: "b", Path: "/b", CACertPath: "/etc/caddy/ca/b.crt", Upstream: "h2c://127.0.0.1:10000", Role: "server"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "trust_pool file /etc/caddy/ca/a.crt /etc/caddy/ca/b.crt") {
		t.Errorf("trust pool should list both CAs on one line\n---\n%s", out)
	}
	if !strings.Contains(out, "@a path /a*") || !strings.Contains(out, "@b path /b*") {
		t.Errorf("both server handle blocks should be present\n---\n%s", out)
	}
}
