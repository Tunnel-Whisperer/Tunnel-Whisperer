package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/pki"
)

func TestParseExpires(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		{"2027-01-02T03:04:05Z", time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC), false},
		{"2027-01-02T04:04:05+01:00", time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC), false},
		{"2026-12-31", time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC), false},
		{"30d", now.Add(30 * 24 * time.Hour), false},
		{"12h", now.Add(12 * time.Hour), false},
		{"90m", now.Add(90 * time.Minute), false},
		{"1h30m", now.Add(90 * time.Minute), false},
		{"30s", time.Time{}, true},
		{"2026-11-16T09:00:00Z", time.Time{}, true},
		{"2026-11-15", time.Time{}, true},
		{"-5d", time.Time{}, true},
		{"soon", time.Time{}, true},
		{"", time.Time{}, true},
	}
	for _, c := range cases {
		got, err := ParseExpires(c.in, now)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseExpires(%q) = %v, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseExpires(%q): %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) || got.Location() != time.UTC {
			t.Errorf("ParseExpires(%q) = %v, want %v UTC", c.in, got, c.want)
		}
	}
	if _, err := ParseExpires("30s", now); err == nil || !strings.Contains(err.Error(), "expiry must be at least 1 minute in the future") {
		t.Errorf("30s error = %v", err)
	}
}

func TestParseExpiresCACap(t *testing.T) {
	o := newExpiryOps(t)
	data, err := os.ReadFile(config.CACertPath())
	if err != nil {
		t.Fatal(err)
	}
	caEnd, err := pki.CertNotAfter(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := o.CheckExpiresAgainstCA(caEnd); err != nil {
		t.Fatalf("at CA end: %v", err)
	}
	err = o.CheckExpiresAgainstCA(caEnd.Add(time.Second))
	if err == nil || !strings.Contains(err.Error(), caEnd.UTC().Format(time.RFC3339)) {
		t.Fatalf("past CA end: %v", err)
	}
}

func TestInviteExpiredNameMessage(t *testing.T) {
	o := newExpiryOps(t)
	grantForTest(t, o, "carol", time.Now().Add(-30*time.Minute))
	grantForTest(t, o, "bob", time.Now().Add(48*time.Hour))
	if err := os.MkdirAll(filepath.Join(config.UsersDir(), "old"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Xray: config.XrayConfig{RelayHost: "relay.example", UUID: "u-1"}}
	req := func(n string) CreateUserRequest {
		return CreateUserRequest{Name: n, Mappings: []config.PortMapping{{ClientPort: 1, ServerPort: 2}}}
	}

	err := validateCreateUser(cfg, req("carol"))
	if err == nil || !strings.Contains(err.Error(), `user "carol" has expired; delete it first (tw server user delete carol)`) {
		t.Fatalf("carol: %v", err)
	}
	for _, n := range []string{"bob", "old"} {
		err = validateCreateUser(cfg, req(n))
		if err == nil || err.Error() != `user "`+n+`" already exists` {
			t.Fatalf("%s: %v", n, err)
		}
	}
}
