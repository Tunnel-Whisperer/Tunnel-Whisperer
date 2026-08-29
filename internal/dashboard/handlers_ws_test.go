package dashboard

import (
	"net/http"
	"testing"
)

func originReq(origin, host string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "http://"+host+"/api/relay/ssh", nil)
	r.Host = host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}

// The relay SSH terminal yields a privileged shell on the relay, so the
// WebSocket upgrade must reject cross-site origins to prevent drive-by hijack.
func TestCheckSameOrigin(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{"same origin", "http://localhost:8080", "localhost:8080", true},
		{"cross-site page", "http://evil.example", "localhost:8080", false},
		{"same host different port", "http://localhost:9999", "localhost:8080", false},
		{"non-browser client (no Origin)", "", "localhost:8080", true},
		{"host case-insensitive", "http://LocalHost:8080", "localhost:8080", true},
		{"malformed origin", "://nope", "localhost:8080", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := checkSameOrigin(originReq(c.origin, c.host)); got != c.want {
				t.Errorf("checkSameOrigin(origin=%q, host=%q) = %v, want %v", c.origin, c.host, got, c.want)
			}
		})
	}
}
