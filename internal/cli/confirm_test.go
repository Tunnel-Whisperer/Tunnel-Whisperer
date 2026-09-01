package cli

import (
	"bufio"
	"strings"
	"testing"
)

// feedStdin points sharedLine at canned input instead of os.Stdin.
func feedStdin(t *testing.T, input string) {
	t.Helper()
	old := sharedScanner
	sharedScanner = bufio.NewScanner(strings.NewReader(input))
	t.Cleanup(func() { sharedScanner = old })
}

func TestConfirmYesAnswered(t *testing.T) {
	feedStdin(t, "y\n")
	yes, err := confirmYes()
	if err != nil || !yes {
		t.Fatalf("answer y: got (%v, %v), want (true, nil)", yes, err)
	}

	feedStdin(t, "n\n")
	yes, err = confirmYes()
	if err != nil || yes {
		t.Fatalf("answer n: got (%v, %v), want (false, nil)", yes, err)
	}
}

// A closed stdin (non-interactive caller) must surface as an error, not read
// as a declined prompt: otherwise the command exits 0 without acting and the
// caller cannot tell (issue #10).
func TestConfirmYesEOFIsError(t *testing.T) {
	feedStdin(t, "")
	if _, err := confirmYes(); err == nil {
		t.Fatal("EOF: got nil error, want non-nil")
	}
}
