package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/api"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var exportUserCmd = &cobra.Command{
	Use:   "export <name>",
	Short: "Export a user as an encrypted client context bundle",
	Long: `Package a user as a role=client context (the same format as tw config
export). The client imports it with: tw client import <file>

Exporting prints a one-time passphrase that seals the bundle; share it with the
client out-of-band (the client needs it to import).`,
	Args: cobra.ExactArgs(1),
	RunE: runExportUser,
}

func init() {
	serverUserCmd.AddCommand(exportUserCmd)
}

func runExportUser(cmd *cobra.Command, args []string) error {
	if err := requireMode("server"); err != nil {
		return err
	}
	name := args[0]

	cfg, _ := config.Load()
	addr := fmt.Sprintf("localhost:%d", cfg.Server.APIPort)

	var data []byte
	var passphrase string

	client, dialErr := api.Dial(addr)
	if dialErr != nil {
		// No daemon running, export locally.
		o, err := ops.New()
		if err != nil {
			return fmt.Errorf("initializing: %w", err)
		}
		data, passphrase, err = o.GetUserConfigBundle(name)
		if err != nil {
			return err
		}
	} else {
		defer client.Close()
		var err error
		data, passphrase, err = client.GetUserConfig(context.Background(), name)
		if err != nil {
			return fmt.Errorf("exporting user config: %w", err)
		}
	}

	filename := name + "-tw-context.twctx"
	outPath := filepath.Join(".", filename)

	if err := os.WriteFile(outPath, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", filename, err)
	}

	fmt.Printf("  Exported %s (%d bytes)\n", filename, len(data))
	fmt.Printf("  Passphrase: %s\n", passphrase)
	fmt.Println("  Share the passphrase out-of-band. The client imports with:")
	fmt.Printf("    tw client import %s\n", filename)
	return nil
}
