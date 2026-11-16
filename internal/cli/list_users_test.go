package cli

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tunnelwhisperer/tw/internal/ops"
)

func TestFormatExpiry(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"past", "2026-11-16T11:59:59Z", "EXPIRED"},
		{"within 7 days", "2026-11-19T13:00:00Z", "2026-11-19T13:00:00Z (in 3d)"},
		{"under a day", "2026-11-17T11:00:00Z", "2026-11-17T11:00:00Z (in <1d)"},
		{"beyond 7 days", "2027-01-01T00:00:00Z", "2027-01-01T00:00:00Z"},
	}
	for _, c := range cases {
		if got := formatExpiry(c.in, now); got != c.want {
			t.Errorf("%s: formatExpiry(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestPrintUsersShowsExpiry(t *testing.T) {
	future := time.Now().UTC().Add(30 * 24 * time.Hour).Format(time.RFC3339)
	out := captureStdout(t, func() {
		printUsers([]ops.UserInfo{
			{Name: "bob", ExpiresAt: future},
			{Name: "carol", ExpiresAt: "2020-01-01T00:00:00Z", Expired: true},
			{Name: "dave"},
		})
	})
	if want := "    Expires: " + future + "\n"; !strings.Contains(out, want) {
		t.Errorf("missing %q in:\n%s", want, out)
	}
	if want := "    Expires: EXPIRED\n"; !strings.Contains(out, want) {
		t.Errorf("missing %q in:\n%s", want, out)
	}
	if n := strings.Count(out, "Expires:"); n != 2 {
		t.Errorf("Expires lines = %d, want 2 (dave has none):\n%s", n, out)
	}
}
