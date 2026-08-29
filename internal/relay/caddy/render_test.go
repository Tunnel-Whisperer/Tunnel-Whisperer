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
		"mode verify_if_given",
		"trust_pool file /etc/caddy/ca/tw-server.crt",
		"protocols tls1.3",
		"path /tw*",
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
	if !strings.Contains(out, "path /a*") || !strings.Contains(out, "path /b*") {
		t.Errorf("both server handle blocks should be present\n---\n%s", out)
	}
}

func TestRenderCaddyfileIsolation(t *testing.T) {
	out, err := RenderCaddyfile(Config{
		Domain: "relay.example.com",
		Servers: []Server{
			{ID: "web-01-a1b2c3d4", Path: "/tw/web-01-a1b2c3d4", CACertPath: "/etc/caddy/ca/web-01-a1b2c3d4.crt", Upstream: "h2c://127.0.0.1:30000", Role: "server"},
			{ID: "db-02-99887766", Path: "/tw/db-02-99887766", CACertPath: "/etc/caddy/ca/db-02-99887766.crt", Upstream: "h2c://127.0.0.1:30001", Role: "server"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"@web-01-a1b2c3d4 {",
		"path /tw/web-01-a1b2c3d4*",
		`expression {http.request.tls.client.subject} == "CN=web-01-a1b2c3d4"`,
		`expression {http.request.tls.client.subject} == "CN=db-02-99887766"`,
		// #10: the route must also pin the issuing CA (CN==id), not just the CN,
		// so a tenant cannot present a victim-CN cert off its own trusted CA.
		`{http.request.tls.client.subject} == "CN=web-01-a1b2c3d4" && {http.request.tls.client.issuer} == "CN=web-01-a1b2c3d4"`,
		`{http.request.tls.client.issuer} == "CN=db-02-99887766"`,
		"reverse_proxy h2c://127.0.0.1:30000",
		"reverse_proxy h2c://127.0.0.1:30001",
		"handle {\n        respond 404\n    }",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n---\n%s", want, out)
		}
	}
	a := out[strings.Index(out, "@web-01-a1b2c3d4"):strings.Index(out, "@db-02-99887766")]
	if strings.Contains(a, "db-02-99887766") {
		t.Errorf("web-01 handle leaks db-02 id:\n%s", a)
	}
}

func TestRenderEnrollRoutes(t *testing.T) {
	out, err := RenderCaddyfile(Config{
		Domain: "relay.example.com",
		Servers: []Server{{
			ID: "host-a1b2c3d4", Path: "/tw/host-a1b2c3d4",
			CACertPath: "/etc/caddy/ca/host-a1b2c3d4.crt",
			Upstream:   "h2c://127.0.0.1:30000", Role: "relay",
			EnrollTok: "a1b2c3d4", EnrollPort: 40000,
		}},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"mode verify_if_given",
		"@enroll_host-a1b2c3d4 path /enroll/a1b2c3d4/*",
		"reverse_proxy 127.0.0.1:40000",
		"handle_errors {",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Caddyfile missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "require_and_verify") {
		t.Error("require_and_verify must be gone — enrollees have no cert yet")
	}
}
