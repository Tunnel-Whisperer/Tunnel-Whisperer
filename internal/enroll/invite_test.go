package enroll

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestMintParseRedeem(t *testing.T) {
	inv, err := Mint("a1b2c3d4", 15*time.Minute)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if !regexp.MustCompile(`^a1b2c3d4-\d{2}-[a-z]+-[a-z]+$`).MatchString(inv.Code) {
		t.Fatalf("code shape: %q", inv.Code)
	}
	tok, err := ParseCode(inv.Code)
	if err != nil || tok != "a1b2c3d4" {
		t.Fatalf("parse: tok=%q err=%v", tok, err)
	}
	if err := inv.Redeem(); err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	if err := inv.Redeem(); err == nil {
		t.Fatal("second redeem should burn")
	}
}

func TestMintExpired(t *testing.T) {
	inv, err := Mint("a1b2c3d4", -time.Second)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if !inv.Expired() {
		t.Fatal("should be expired")
	}
	if err := inv.Redeem(); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("redeem of expired invite: %v", err)
	}
}

func TestParseCodeRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "nodashes", "a1b2c3d4-47-only"} {
		if _, err := ParseCode(bad); err == nil {
			t.Fatalf("ParseCode(%q) accepted", bad)
		}
	}
}

func TestWordlistIs256(t *testing.T) {
	if len(words) != 256 {
		t.Fatalf("wordlist has %d words, want 256", len(words))
	}
}
