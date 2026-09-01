package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// relayCmd groups everything the relay owner (relay profile) does to the
// relay: provision, destroy, inspect, and register server tenants. The
// commands are gated to the relay mode; the group is named after the object
// they manage.
var relayCmd = &cobra.Command{
	Use:   "relay",
	Short: "Manage the relay server (relay role)",
}

func init() {
	rootCmd.AddCommand(relayCmd)
}

// sharedScanner backs non-interactive (piped) input. A single shared scanner
// avoids each call over-buffering os.Stdin and swallowing later lines.
var sharedScanner *bufio.Scanner

func sharedLine() (string, bool) {
	if sharedScanner == nil {
		sharedScanner = bufio.NewScanner(os.Stdin)
	}
	if sharedScanner.Scan() {
		return strings.TrimSpace(sharedScanner.Text()), true
	}
	return "", false
}

// confirmYes reads the answer to a [y/N] prompt. A closed stdin (EOF before
// any answer — a non-interactive caller) is an error, not a "no": silently
// taking the default would leave the command exiting 0 without acting.
func confirmYes() (bool, error) {
	ans, ok := sharedLine()
	if !ok {
		fmt.Println()
		return false, errors.New("stdin closed before the prompt was answered")
	}
	return strings.EqualFold(ans, "y"), nil
}

