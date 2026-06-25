package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
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

// readSecret reads one secret. On a real terminal it reads with echo off; when
// stdin is piped (tests/scripts) it reads a plain line from the shared scanner.
func readSecret(label string) (string, error) {
	fmt.Printf("  %s: ", label)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Println()
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", label, err)
		}
		return string(b), nil
	}
	line, ok := sharedLine()
	if !ok {
		return "", fmt.Errorf("no input provided for %s", label)
	}
	return line, nil
}

func promptNewPassphrase() (string, error) {
	p1, err := readSecret("Set a passphrase to encrypt the bundle")
	if err != nil {
		return "", err
	}
	if p1 == "" {
		return "", fmt.Errorf("passphrase cannot be empty")
	}
	p2, err := readSecret("Confirm passphrase")
	if err != nil {
		return "", err
	}
	if p1 != p2 {
		return "", fmt.Errorf("passphrases do not match")
	}
	return p1, nil
}
