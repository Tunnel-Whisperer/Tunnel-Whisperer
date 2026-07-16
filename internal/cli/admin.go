package cli

import (
	"bufio"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var adminCmd = &cobra.Command{
	Use:   "admin",
	Short: "Admin-mode relay ownership commands",
}

func init() {
	rootCmd.AddCommand(adminCmd)
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

