package xray

import (
	"encoding/json"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

// TestRedactProxyURL is the #11 regression: proxy credentials must never reach
// the logs. The userinfo is stripped; scheme/host stay for debuggability.
func TestRedactProxyURL(t *testing.T) {
	cases := map[string]string{
		"":                                     "",
		"socks5://host:1080":                   "socks5://host:1080",
		"socks5://user:pass@host:1080":         "socks5://redacted@host:1080",
		"http://alice:s3cr3t@proxy.local:3128": "http://redacted@proxy.local:3128",
	}
	for in, want := range cases {
		if got := redactProxyURL(in); got != want {
			t.Errorf("redactProxyURL(%q) = %q, want %q", in, got, want)
		}
	}
	// A value with a password must not leak the secret regardless of exact form.
	if got := redactProxyURL("socks5://user:topsecret@host:1080"); got == "socks5://user:topsecret@host:1080" || contains(got, "topsecret") {
		t.Errorf("redactProxyURL leaked the password: %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func firstServer(t *testing.T, out map[string]interface{}) map[string]interface{} {
	t.Helper()
	servers := out["settings"].(map[string]interface{})["servers"].([]map[string]interface{})
	if len(servers) != 1 {
		t.Fatalf("got %d servers, want 1", len(servers))
	}
	return servers[0]
}

func TestProxyOutboundDefaultPort(t *testing.T) {
	cases := map[string]string{"socks5://p": "socks", "http://p": "http"}
	for in, wantProto := range cases {
		out, err := proxyOutbound(in)
		if err != nil {
			t.Fatalf("proxyOutbound(%q): %v", in, err)
		}
		if out["protocol"] != wantProto {
			t.Errorf("proxyOutbound(%q) protocol = %v, want %q", in, out["protocol"], wantProto)
		}
		if out["tag"] != "proxy-out" {
			t.Errorf("proxyOutbound(%q) tag = %v, want proxy-out", in, out["tag"])
		}
		if got := firstServer(t, out)["port"]; got != 1080 {
			t.Errorf("proxyOutbound(%q) port = %v, want 1080", in, got)
		}
	}
}

func TestProxyOutboundDecodesUserinfo(t *testing.T) {
	out, err := proxyOutbound("http://u:p%40ss@p:3128")
	if err != nil {
		t.Fatal(err)
	}
	srv := firstServer(t, out)
	if srv["port"] != 3128 || srv["address"] != "p" {
		t.Errorf("server = %v, want p:3128", srv)
	}
	users := srv["users"].([]map[string]interface{})
	if len(users) != 1 || users[0]["user"] != "u" || users[0]["pass"] != "p@ss" {
		t.Errorf("users = %v, want [{u p@ss}]", users)
	}
}

func TestBuildClientConfigWiresDialerProxy(t *testing.T) {
	cfg := config.XrayConfig{RelayHost: "relay.example", RelayPort: 443, UUID: "00000000-0000-0000-0000-000000000000", Path: "/x"}
	build := func(proxy string) map[string]interface{} {
		raw, err := buildClientConfig(cfg, config.ClientConfig{}, 54001, proxy)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]interface{}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	outbound := func(m map[string]interface{}, tag string) map[string]interface{} {
		for _, o := range m["outbounds"].([]interface{}) {
			if ob := o.(map[string]interface{}); ob["tag"] == tag {
				return ob
			}
		}
		return nil
	}

	with := build("socks5://p:1080")
	if outbound(with, "proxy-out") == nil {
		t.Fatal("proxy-out outbound missing")
	}
	ss := outbound(with, "to-relay")["streamSettings"].(map[string]interface{})
	if got := ss["sockopt"].(map[string]interface{})["dialerProxy"]; got != "proxy-out" {
		t.Errorf("dialerProxy = %v, want proxy-out", got)
	}

	without := build("")
	if outbound(without, "proxy-out") != nil {
		t.Error("proxy-out present without proxy")
	}
	relay := outbound(without, "to-relay")
	if relay == nil {
		t.Fatal("to-relay outbound missing without proxy")
	}
	if ss, ok := relay["streamSettings"].(map[string]interface{}); ok {
		if _, has := ss["sockopt"]; has {
			t.Error("sockopt present without proxy")
		}
	}
}
