package cli

import "testing"

func TestParseOnOff(t *testing.T) {
	if v, err := parseOnOff("on"); err != nil || !v {
		t.Errorf("on: got (%v, %v)", v, err)
	}
	if v, err := parseOnOff("off"); err != nil || v {
		t.Errorf("off: got (%v, %v)", v, err)
	}
	for _, bad := range []string{"", "true", "ON ", "yes"} {
		if _, err := parseOnOff(bad); err == nil {
			t.Errorf("want error for %q", bad)
		}
	}
}
