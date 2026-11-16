package ops

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/pki"
)

// ParseExpires turns a user-supplied expiry into an absolute UTC time. It
// accepts RFC 3339, YYYY-MM-DD (that day 23:59:59 UTC), "<n>d" (days) and any
// time.ParseDuration string, the latter two relative to now.
func ParseExpires(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	var t time.Time
	if p, err := time.Parse(time.RFC3339, s); err == nil {
		t = p
	} else if d, err := time.Parse("2006-01-02", s); err == nil {
		t = time.Date(d.Year(), d.Month(), d.Day(), 23, 59, 59, 0, time.UTC)
	} else {
		dur, err := parseExpiryDuration(s)
		if errors.Is(err, errExpiryTooFar) {
			return time.Time{}, err
		}
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid expiry %q: want RFC 3339, YYYY-MM-DD, or a duration like 30d/12h", s)
		}
		t = now.Add(dur)
	}
	t = t.UTC().Truncate(time.Second)
	if t.Before(now.Truncate(time.Second).Add(time.Minute)) {
		return time.Time{}, fmt.Errorf("expiry must be at least 1 minute in the future")
	}
	return t, nil
}

// maxExpiryDays bounds "<n>d" (100 years) so the conversion cannot overflow.
const maxExpiryDays = 36500

var errExpiryTooFar = errors.New("expiry too far in the future")

func parseExpiryDuration(s string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil {
			return 0, err
		}
		// Refuse before multiplying: a large n wraps time.Duration silently.
		if days > maxExpiryDays {
			return 0, errExpiryTooFar
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// CheckExpiresAfterTTL refuses an expiry that could be past, or under a
// minute away, by the time the invite is redeemed: the cert is signed at
// redemption, up to ttl after now.
func CheckExpiresAfterTTL(exp time.Time, ttl time.Duration, now time.Time) error {
	if !exp.After(now.Add(ttl + time.Minute)) {
		return fmt.Errorf("--expires %s must be more than --ttl (%s) plus 1 minute from now; the certificate is signed when the invite is redeemed",
			exp.UTC().Format(time.RFC3339), ttl)
	}
	return nil
}

// CheckExpiresAgainstCA refuses an expiry later than the server CA's own
// NotAfter, since a client cert cannot outlive its issuer.
func (o *Ops) CheckExpiresAgainstCA(t time.Time) error {
	data, err := os.ReadFile(config.CACertPath())
	if err != nil {
		return fmt.Errorf("reading CA certificate: %w", err)
	}
	caEnd, err := pki.CertNotAfter(data)
	if err != nil {
		return fmt.Errorf("reading CA expiry: %w", err)
	}
	if t.After(caEnd) {
		return fmt.Errorf("expiry %s is after the server CA's own expiry %s",
			t.UTC().Format(time.RFC3339), caEnd.UTC().Format(time.RFC3339))
	}
	return nil
}
