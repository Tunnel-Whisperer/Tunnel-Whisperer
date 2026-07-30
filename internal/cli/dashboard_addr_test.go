package cli

import "testing"

func TestResolveDashboardAddr(t *testing.T) {
	cases := []struct {
		name, flag, cfg string
		port            int
		want            string
	}{
		{"default loopback", "", "", 8080, "127.0.0.1:8080"},
		{"config override", "", "0.0.0.0", 8080, "0.0.0.0:8080"},
		{"flag beats config", "10.0.0.5", "0.0.0.0", 9000, "10.0.0.5:9000"},
		{"flag alone", "0.0.0.0", "", 8080, "0.0.0.0:8080"},
	}
	for _, c := range cases {
		if got := resolveDashboardAddr(c.flag, c.cfg, c.port); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
