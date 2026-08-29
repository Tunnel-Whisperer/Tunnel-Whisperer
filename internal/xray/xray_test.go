package xray

import "testing"

// TestRedactProxyURL is the #11 regression: proxy credentials must never reach
// the logs. The userinfo is stripped; scheme/host stay for debuggability.
func TestRedactProxyURL(t *testing.T) {
	cases := map[string]string{
		"":                                    "",
		"socks5://host:1080":                  "socks5://host:1080",
		"socks5://user:pass@host:1080":        "socks5://redacted@host:1080",
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
