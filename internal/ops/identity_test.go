package ops

import "testing"

func TestSanitizeHostname(t *testing.T) {
	cases := map[string]string{
		"Web-01.corp.local": "web-01-corp-local",
		"  My Host! ":        "my-host",
		"":                   "tw",
		"---":                "tw",
		"ALLCAPS":            "allcaps",
	}
	for in, want := range cases {
		if got := sanitizeHostname(in); got != want {
			t.Errorf("sanitizeHostname(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeriveServerID(t *testing.T) {
	if id := deriveServerID("Web-01", "a1b2c3d4-aaaa-bbbb-cccc-ddddeeeeffff"); id != "web-01-a1b2c3d4" {
		t.Errorf("deriveServerID = %q, want web-01-a1b2c3d4", id)
	}
	if id := deriveServerID("", "a1b2c3d4-xxxx"); id != "tw-a1b2c3d4" {
		t.Errorf("deriveServerID empty host = %q, want tw-a1b2c3d4", id)
	}
}

func TestFirstFreeFromBase(t *testing.T) {
	p, err := firstFreeFromBase(20000, []int{20000, 20001, 20003})
	if err != nil || p != 20002 {
		t.Fatalf("firstFreeFromBase = %d, %v; want 20002, nil", p, err)
	}
	if p, _ := firstFreeFromBase(20000, nil); p != 20000 {
		t.Errorf("empty used = %d, want 20000", p)
	}
}
