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
	Use:   "export-user <name>",
	Short: "Issue a user as an encrypted client context bundle (server only)",
	Long: `Package one of this server's users as a role=client context (the same
format as tw config export). The client imports it with: tw config import <file>

The bundle carries no passphrase — the client imports it without a prompt. It is
as sensitive as the keys inside it, so send it over a trusted channel.`,
	Args: cobra.ExactArgs(1),
	RunE: runExportUser,
}

func init() {
	configCmd.AddCommand(exportUserCmd)
}

func runExportUser(cmd *cobra.Command, args []string) error {
	if err := requireMode("server"); err != nil {
		return err
	}
	name := args[0]

	cfg, _ := config.Load()
	addr := fmt.Sprintf("localhost:%d", cfg.Server.APIPort)

	var data []byte

	client, dialErr := api.Dial(addr)
	if dialErr != nil {
		// No daemon running, export locally.
		o, err := ops.New()
		if err != nil {
			return fmt.Errorf("initializing: %w", err)
		}
		data, err = o.GetUserConfigBundle(name)
		if err != nil {
			return err
		}
	} else {
		defer client.Close()
		var err error
		data, err = client.GetUserConfig(context.Background(), name)
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
	fmt.Println("  Send this file to the client (it needs no passphrase to import).")
	fmt.Println("  The client imports it with:")
	fmt.Printf("    tw config import %s --activate\n", filename)
	return nil
}
