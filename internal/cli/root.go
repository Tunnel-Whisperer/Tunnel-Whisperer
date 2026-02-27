package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/logging"
)

var logLevel string

var rootCmd = &cobra.Command{
	Use:   "tw",
	Short: "Tunnel Whisperer — surgical, resilient connectivity",
	Long: `Tunnel Whisperer creates resilient, application-layer bridges for specific
ports across separated private networks. It encapsulates traffic in standard
HTTPS/WebSocket to traverse strict firewalls and DPI.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if cmd.Flags().Changed("log-level") {
			// Explicit flag — persist to config so the dashboard stays in sync.
			if cfg, err := config.Load(); err == nil {
				cfg.LogLevel = logLevel
				config.Save(cfg)
			}
		} else {
			// No flag — use config's log level if set.
			if cfg, err := config.Load(); err == nil && cfg.LogLevel != "" {
				logLevel = cfg.LogLevel
			}
		}
		logging.Setup(logLevel)
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error)")
}

func Execute() error {
	return rootCmd.Execute()
}

// requireMode returns an error if the current config mode doesn't match the
// expected mode. This prevents running server-only commands in client mode
// and vice versa.
func requireMode(expected string) error {
	cfg, err := config.Load()
	if err != nil {
		return nil // can't determine mode, let the command proceed
	}
	if cfg.Mode == "" {
		return nil // mode not set yet, allow
	}
	if cfg.Mode != expected {
		other := "server"
		if expected == "server" {
			other = "client"
		}
		return fmt.Errorf("this is a %s command, but tw is configured in %s mode", expected, other)
	}
	return nil
}
