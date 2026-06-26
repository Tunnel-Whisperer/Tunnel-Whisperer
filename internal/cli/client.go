package cli

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var clientCmd = &cobra.Command{
	Use:   "client",
	Short: "Client-mode commands",
}

var clientListenCmd = &cobra.Command{
	Use:   "listen [address]",
	Short: "Show or set the local interface tunnels bind to",
	Long: `Show the current listen address, or set it to a new value.

With no argument, prints the configured address (defaults to 127.0.0.1).
Pass an IP to change it. Use 0.0.0.0 to expose tunnels on all interfaces
(required when running tw in a container that publishes ports to the host).

Takes effect on next reconnect.

Examples:
  tw client listen              Show current listen address
  tw client listen 0.0.0.0      Bind tunnels to all interfaces
  tw client listen 127.0.0.1    Restore the default (local only)`,
	Args: cobra.MaximumNArgs(1),
	RunE: runClientListen,
}

var clientImportCmd = &cobra.Command{
	Use:   "import <user-bundle.zip>",
	Short: "Install a server-issued user bundle so this machine can connect",
	Long: `Extract the zip your server gave you (tw server user export) into the
config directory, then connect with: tw client connect

Use --working-directory to extract into a specific folder (and pass the same
--working-directory to later commands), e.g.:
  tw client import alice.zip --working-directory ./alice
  tw client connect --working-directory ./alice`,
	Args: cobra.ExactArgs(1),
	RunE: runClientImport,
}

func init() {
	clientCmd.AddCommand(clientListenCmd, clientImportCmd)
	rootCmd.AddCommand(clientCmd)
}

func runClientImport(cmd *cobra.Command, args []string) error {
	data, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("reading bundle: %w", err)
	}
	if _, err := os.Stat(config.FilePath()); err == nil {
		fmt.Printf("  A config already exists at %s and will be overwritten. Continue? [y/N]: ", config.Dir())
		if ans, _ := sharedLine(); strings.ToLower(ans) != "y" {
			fmt.Println("  Aborted.")
			return nil
		}
	}
	if err := ops.ImportUserBundle(data); err != nil {
		return err
	}
	fmt.Printf("  User bundle imported into %s\n", config.Dir())
	fmt.Println("  Connect with: tw client connect")
	return nil
}

func runClientListen(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		addr := cfg.Client.ListenAddress
		if addr == "" {
			addr = "127.0.0.1"
		}
		fmt.Printf("  Listen address: %s\n", addr)
		return nil
	}

	addr := args[0]
	if ip := net.ParseIP(addr); ip == nil {
		return fmt.Errorf("invalid IP address: %s", addr)
	}

	o, err := ops.New()
	if err != nil {
		return err
	}
	if err := o.SetClientListenAddress(addr); err != nil {
		return err
	}
	fmt.Printf("  Listen address set to: %s\n", addr)
	fmt.Println("  (takes effect on next reconnect)")
	return nil
}
