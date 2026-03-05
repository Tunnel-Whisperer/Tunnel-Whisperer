package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/logging"
	"github.com/tunnelwhisperer/tw/internal/version"
)

var logLevel string
var logFormat string

var rootCmd = &cobra.Command{
	Use:     "tw",
	Short:   "Tunnel Whisperer — surgical, resilient connectivity",
	Version: version.Version,
	Long: `Tunnel Whisperer creates resilient, application-layer bridges for specific
ports across separated private networks. It encapsulates traffic in standard
HTTPS/WebSocket to traverse strict firewalls and DPI.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if cmd.Flags().Changed("log-level") {
			if cfg, err := config.Load(); err == nil {
				cfg.LogLevel = logLevel
				config.Save(cfg)
			}
		} else {
			if cfg, err := config.Load(); err == nil && cfg.LogLevel != "" {
				logLevel = cfg.LogLevel
			}
		}
		if cmd.Flags().Changed("log-format") {
			if cfg, err := config.Load(); err == nil {
				cfg.LogFormat = logFormat
				config.Save(cfg)
			}
		} else {
			if cfg, err := config.Load(); err == nil && cfg.LogFormat != "" {
				logFormat = cfg.LogFormat
			}
		}
		logging.Setup(logLevel, logFormat)
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().StringVar(&logFormat, "log-format", "text", "log format (text, json)")
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
