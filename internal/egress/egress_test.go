package egress

import (
	"strings"
	"testing"
)

func mapLookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestParseURL(t *testing.T) {
	good := []struct{ in, host, user string }{
		{"socks5://p", "p:1080", ""},
		{"http://p", "p:1080", ""},
		{"http://u:s@p:8080", "p:8080", "u"},
	}
	for _, c := range good {
		u, err := ParseURL(c.in)
		if err != nil {
			t.Fatalf("ParseURL(%q): %v", c.in, err)
		}
		if u.Host != c.host {
			t.Errorf("ParseURL(%q) host = %q, want %q", c.in, u.Host, c.host)
		}
		if u.User.Username() != c.user {
			t.Errorf("ParseURL(%q) user = %q, want %q", c.in, u.User.Username(), c.user)
		}
	}
	for _, in := range []string{"ftp://p", "p:3128", "http://", ""} {
		if _, err := ParseURL(in); err == nil {
			t.Errorf("ParseURL(%q) expected error", in)
		}
	}
}

func TestResolvePrecedence(t *testing.T) {
	cases := []struct {
		name   string
		cfg    string
		env    map[string]string
		source Source
		envVar string
		host   string
	}{
		{"config wins", "http://c:1", map[string]string{"HTTPS_PROXY": "socks5://e:2"}, SourceConfig, "", "c:1"},
		{"env order", "", map[string]string{"https_proxy": "http://l:3", "ALL_PROXY": "http://a:4"}, SourceEnv, "https_proxy", "l:3"},
		{"all_proxy only", "", map[string]string{"ALL_PROXY": "http://a:4"}, SourceEnv, "ALL_PROXY", "a:4"},
		{"empty value skipped", "", map[string]string{"HTTPS_PROXY": "", "all_proxy": "http://a:4"}, SourceEnv, "all_proxy", "a:4"},
		{"HTTP_PROXY ignored", "", map[string]string{"HTTP_PROXY": "http://h:5"}, SourceNone, "", ""},
		{"nothing", "", nil, SourceNone, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := Resolve(c.cfg, mapLookup(c.env))
			if err != nil {
				t.Fatal(err)
			}
			if p.Source != c.source || p.EnvVar != c.envVar {
				t.Fatalf("got source=%q envvar=%q, want %q %q", p.Source, p.EnvVar, c.source, c.envVar)
			}
			if c.host == "" {
				if p.URL != nil {
					t.Fatalf("URL = %v, want nil", p.URL)
				}
			} else if p.URL == nil || p.URL.Host != c.host {
				t.Fatalf("URL = %v, want host %q", p.URL, c.host)
			}
		})
	}
}

func TestResolveEnvInvalidNamesVariable(t *testing.T) {
	_, err := Resolve("", mapLookup(map[string]string{"HTTPS_PROXY": "ftp://x"}))
	if err == nil || !strings.Contains(err.Error(), "HTTPS_PROXY") || !strings.Contains(err.Error(), "ftp") {
		t.Fatalf("err = %v, want it to name HTTPS_PROXY and ftp", err)
	}
	if _, err := Resolve("http://ok:1", mapLookup(map[string]string{"HTTPS_PROXY": "ftp://x"})); err != nil {
		t.Fatalf("env should be ignored when config set: %v", err)
	}
	_, err = Resolve("ftp://bad", mapLookup(nil))
	if err == nil || !strings.Contains(err.Error(), "config") {
		t.Fatalf("err = %v, want it to mention config", err)
	}
}

func TestStringRedactsUserinfo(t *testing.T) {
	p, err := Resolve("http://user:p%40ss@proxy:3128", mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	got := p.String()
	if got != "http://redacted@proxy:3128 (source: config)" {
		t.Errorf("String() = %q", got)
	}
	if strings.Contains(got, "p%40ss") || strings.Contains(got, "p@ss") {
		t.Errorf("String() leaks password: %q", got)
	}
	p, err = Resolve("", mapLookup(map[string]string{"ALL_PROXY": "socks5://proxy"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.String(); got != "socks5://proxy:1080 (source: env ALL_PROXY)" {
		t.Errorf("String() = %q", got)
	}
	p, _ = Resolve("", mapLookup(nil))
	if got := p.String(); got != "none" {
		t.Errorf("String() = %q", got)
	}
}

func TestForHostNoProxyOnlyForEnvSource(t *testing.T) {
	env, err := Resolve("", mapLookup(map[string]string{"HTTPS_PROXY": "http://p:1", "NO_PROXY": "relay.tw.test"}))
	if err != nil {
		t.Fatal(err)
	}
	if env.ForHost("relay.tw.test:443") != nil {
		t.Error("NO_PROXY host should be direct for env source")
	}
	if env.ForHost("relay.tw.test") != nil {
		t.Error("bare NO_PROXY host should be direct for env source")
	}
	if env.ForHost("other.example:443") == nil {
		t.Error("other host should be proxied")
	}
	cfg, err := Resolve("http://p:1", mapLookup(map[string]string{"NO_PROXY": "relay.tw.test"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ForHost("relay.tw.test:443") == nil {
		t.Error("NO_PROXY must be ignored for config source")
	}
	lower, err := Resolve("", mapLookup(map[string]string{"HTTPS_PROXY": "http://p:1", "no_proxy": "relay.tw.test"}))
	if err != nil {
		t.Fatal(err)
	}
	if lower.ForHost("relay.tw.test:443") != nil {
		t.Error("lowercase no_proxy should be honoured")
	}
}

func TestForHostLoopbackAlwaysDirect(t *testing.T) {
	p, err := Resolve("socks5://p:1080", mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"127.0.0.1:59000", "localhost:8080", "[::1]:22"} {
		if p.ForHost(h) != nil {
			t.Errorf("ForHost(%q) should be direct", h)
		}
	}
	if p.ForHost("relay.tw.test:443") == nil {
		t.Error("non-loopback should be proxied")
	}
	none, _ := Resolve("", mapLookup(nil))
	if none.ForHost("relay.tw.test:443") != nil {
		t.Error("none source must be direct")
	}
}

func TestSubprocessEnv(t *testing.T) {
	eq := func(got, want []string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	p, err := Resolve("http://u:s@p", mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"HTTPS_PROXY=http://u:s@p:1080", "HTTP_PROXY=http://u:s@p:1080", "NO_PROXY=", "no_proxy="}
	if got := p.SubprocessEnv(); !eq(got, want) {
		t.Errorf("config SubprocessEnv() = %v, want %v", got, want)
	}
	e, _ := Resolve("", mapLookup(map[string]string{"HTTPS_PROXY": "http://p:1"}))
	if e.SubprocessEnv() != nil {
		t.Error("HTTPS_PROXY env source must yield nil")
	}
	for _, name := range []string{"ALL_PROXY", "all_proxy"} {
		a, _ := Resolve("", mapLookup(map[string]string{name: "socks5://p:1"}))
		want := []string{"HTTPS_PROXY=socks5://p:1", "HTTP_PROXY=socks5://p:1"}
		if got := a.SubprocessEnv(); !eq(got, want) {
			t.Errorf("%s SubprocessEnv() = %v, want %v", name, got, want)
		}
	}
	n, _ := Resolve("", mapLookup(nil))
	if n.SubprocessEnv() != nil {
		t.Error("none source must yield nil")
	}
}

func TestURLFor(t *testing.T) {
	env := map[string]string{"HTTPS_PROXY": "http://p:1", "NO_PROXY": "relay.tw.test"}
	e, err := Resolve("", mapLookup(env))
	if err != nil {
		t.Fatal(err)
	}
	if got := e.URLFor("relay.tw.test:443"); got != "" {
		t.Errorf("NO_PROXY host: URLFor = %q, want empty", got)
	}
	if got := e.URLFor("other:443"); got != "http://p:1" {
		t.Errorf("URLFor(other) = %q", got)
	}
	c, err := Resolve("http://p:1", mapLookup(map[string]string{"NO_PROXY": "relay.tw.test"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.URLFor("relay.tw.test:443"); got != "http://p:1" {
		t.Errorf("config source must ignore NO_PROXY, got %q", got)
	}
}

func TestResolveEnvLenientScheme(t *testing.T) {
	for in, want := range map[string]string{
		"proxy.corp:3128": "http://proxy.corp:3128",
		"10.0.0.1:3128":   "http://10.0.0.1:3128",
		"proxy.corp":      "http://proxy.corp:1080",
	} {
		p, err := Resolve("", mapLookup(map[string]string{"HTTPS_PROXY": in}))
		if err != nil || p.URLString() != want {
			t.Errorf("%q: got %q, %v; want %q", in, p.URLString(), err, want)
		}
	}
	if _, err := Resolve("", mapLookup(map[string]string{"HTTPS_PROXY": "ftp://x"})); err == nil || !strings.Contains(err.Error(), "HTTPS_PROXY") {
		t.Errorf("ftp env err = %v", err)
	}
	if _, err := Resolve("proxy.corp:3128", mapLookup(nil)); err == nil || !strings.Contains(err.Error(), "unsupported proxy scheme") {
		t.Errorf("config no-scheme err = %v", err)
	}
}

func TestResolveParseErrorDoesNotLeakCredentials(t *testing.T) {
	_, err := Resolve("", mapLookup(map[string]string{"HTTPS_PROXY": "http://u:secret@p:abc"}))
	if err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "HTTPS_PROXY") {
		t.Fatalf("err = %v, want error naming HTTPS_PROXY without the password", err)
	}
	_, err = Resolve("http://u:secret@p:abc", mapLookup(nil))
	if err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "config") {
		t.Fatalf("err = %v, want error naming config without the password", err)
	}
}

func TestResolveParseErrorHidesPasswordFragments(t *testing.T) {
	const msg = "invalid proxy URL (percent-encode special characters in the user/password)"
	for _, raw := range []string{"http://user:pa/ss@host:3128", "http://user:se?cret@host", "http://user:ab#cd@host"} {
		_, err := Resolve("", mapLookup(map[string]string{"HTTPS_PROXY": raw}))
		if err == nil || err.Error() != "HTTPS_PROXY: "+msg {
			t.Errorf("env %q: err = %v", raw, err)
		}
		_, err = Resolve(raw, mapLookup(nil))
		if err == nil || err.Error() != "config proxy: "+msg {
			t.Errorf("config %q: err = %v", raw, err)
		}
	}
}

func TestParseURLSocks5hAlias(t *testing.T) {
	u, err := ParseURL("socks5h://proxy.corp:1080")
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "socks5" || u.Host != "proxy.corp:1080" {
		t.Fatalf("ParseURL(socks5h) = scheme %q host %q, want socks5 proxy.corp:1080", u.Scheme, u.Host)
	}
	p, err := Resolve("", mapLookup(map[string]string{"ALL_PROXY": "socks5h://p"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.URL.String(); got != "socks5://p:1080" {
		t.Fatalf("Resolve(ALL_PROXY=socks5h://p) URL = %q, want socks5://p:1080", got)
	}
}
