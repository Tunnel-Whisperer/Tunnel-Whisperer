package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
	"golang.org/x/term"
)

var adminCmd = &cobra.Command{
	Use:   "admin",
	Short: "Admin-mode relay ownership commands",
}

var adminExportBundleCmd = &cobra.Command{
	Use:   "export-bundle",
	Short: "Re-export the password-protected admin bundle",
	RunE:  runAdminExportBundle,
}

var adminImportCmd = &cobra.Command{
	Use:   "import <bundle.zip>",
	Short: "Import an admin bundle to manage its relay from this machine",
	Args:  cobra.ExactArgs(1),
	RunE:  runAdminImport,
}

func init() {
	adminCmd.AddCommand(adminExportBundleCmd)
	adminCmd.AddCommand(adminImportCmd)
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
	p1, err := readSecret("Set a passphrase to encrypt the admin bundle")
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

// writeAdminBundle prompts for a passphrase, creates the encrypted bundle, and
// writes it as tw_<domain>_admin.zip in the current directory.
func writeAdminBundle(o *ops.Ops, domain string) error {
	pass, err := promptNewPassphrase()
	if err != nil {
		return err
	}
	data, err := o.CreateAdminBundle(pass)
	if err != nil {
		return err
	}
	safe := strings.NewReplacer(".", "_", ":", "_", "/", "_").Replace(domain)
	fname := fmt.Sprintf("tw_%s_admin.zip", safe)
	if err := os.WriteFile(fname, data, 0600); err != nil {
		return fmt.Errorf("writing bundle: %w", err)
	}
	abs, _ := filepath.Abs(fname)
	fmt.Printf("\n  Admin bundle written: %s\n", abs)
	fmt.Println("  IMPORTANT: back this up securely. It is the only key to managing your relay")
	fmt.Println("  and there is no recovery if it is lost. Anyone with this file AND its")
	fmt.Println("  passphrase can manage your relay.")
	return nil
}

func runAdminExportBundle(cmd *cobra.Command, args []string) error {
	if err := requireMode("admin"); err != nil {
		return err
	}
	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}
	cfg := o.Config()
	if cfg.Xray.RelayHost == "" {
		return fmt.Errorf("no relay configured; provision a relay first")
	}
	return writeAdminBundle(o, cfg.Xray.RelayHost)
}

func runAdminImport(cmd *cobra.Command, args []string) error {
	data, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("reading bundle file: %w", err)
	}
	if _, err := os.Stat(config.FilePath()); err == nil {
		fmt.Print("  A config already exists here and will be overwritten. Continue? [y/N]: ")
		if ans, _ := sharedLine(); strings.ToLower(ans) != "y" {
			fmt.Println("  Aborted.")
			return nil
		}
	}
	pass, err := readSecret("Bundle passphrase")
	if err != nil {
		return err
	}
	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}
	if err := o.ImportAdminBundle(data, pass); err != nil {
		return err
	}
	fmt.Println("  Admin bundle imported. This machine is now configured as admin for the relay.")
	fmt.Println("  You can now open an SSH session to the relay to manage it.")
	return nil
}
