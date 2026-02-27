package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var connectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Connect to a relay as a client",
	RunE:  runConnect,
}

func init() {
	rootCmd.AddCommand(connectCmd)
}

func runConnect(cmd *cobra.Command, args []string) error {
	if err := requireMode("client"); err != nil {
		return err
	}
	fmt.Println("Connecting to relay...")

	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	fmt.Printf("Config: %s\n", config.FilePath())

	if err := o.StartClient(cliProgress); err != nil {
		return err
	}

	fmt.Println("Client connected. Press Ctrl-C to stop.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	fmt.Println("\nDisconnecting...")
	o.StopClient(nil)
	return nil
}
