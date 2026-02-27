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

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export resources",
}

var exportUserCmd = &cobra.Command{
	Use:   "user <name>",
	Short: "Export a user's config bundle as a zip file",
	Args:  cobra.ExactArgs(1),
	RunE:  runExportUser,
}

func init() {
	exportCmd.AddCommand(exportUserCmd)
	rootCmd.AddCommand(exportCmd)
}

func runExportUser(cmd *cobra.Command, args []string) error {
	if err := requireMode("server"); err != nil {
		return err
	}
	name := args[0]

	cfg, _ := config.Load()
	addr := fmt.Sprintf("localhost:%d", cfg.Server.APIPort)

	var data []byte
	var err error

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
		data, err = client.GetUserConfig(context.Background(), name)
		if err != nil {
			return fmt.Errorf("exporting user config: %w", err)
		}
	}

	filename := name + "-tw-config.zip"
	outPath := filepath.Join(".", filename)

	if err := os.WriteFile(outPath, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", filename, err)
	}

	fmt.Printf("  Exported %s (%d bytes)\n", filename, len(data))
	return nil
}
