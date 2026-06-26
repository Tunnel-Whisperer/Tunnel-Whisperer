// Package cli defines the Cobra command tree for the tw binary, with one file
// per command. The commands are thin front-ends that call into internal/ops,
// and many are interactive wizards. root.go also enforces the server/client
// mode gate via requireMode.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/logging"
	"github.com/tunnelwhisperer/tw/internal/version"
)

var logLevel string
var logFormat string
var workingDir string

var rootCmd = &cobra.Command{
	Use:     "tw",
	Short:   "Tunnel Whisperer — surgical, resilient connectivity",
	Version: version.Version,
	Long: `Tunnel Whisperer creates resilient, application-layer bridges for specific
ports across separated private networks. It encapsulates traffic in standard
HTTPS/WebSocket to traverse strict firewalls and DPI.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// --working-directory overrides the config/state dir for every command;
		// must be applied before any config.Load() below (config.Dir reads the env).
		if workingDir != "" {
			wd := filepath.Clean(workingDir)
			os.Setenv("TW_CONFIG_DIR", wd)
			_ = os.MkdirAll(wd, 0o755)
		}
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
	rootCmd.PersistentFlags().StringVar(&workingDir, "working-directory", "", "config/state directory to use instead of the system default (no permissions needed)")
}

func Execute() error {
	return rootCmd.Execute()
}

// requireMode returns an error if the current config mode is set and is not one
// of the allowed modes. An unset mode is always permitted (setup not done yet).
// Variadic so a command can be allowed in more than one mode.
func requireMode(allowed ...string) error {
	cfg, err := config.Load()
	if err != nil {
		return nil // can't determine mode, let the command proceed
	}
	return modeError(cfg.Mode, allowed)
}

// modeError is the pure decision behind requireMode, split out for testing:
// nil if current is unset or present in allowed, otherwise a descriptive error.
func modeError(current string, allowed []string) error {
	if current == "" {
		return nil // mode not set yet, allow
	}
	for _, a := range allowed {
		if current == a {
			return nil
		}
	}
	return fmt.Errorf("this command requires %s mode, but tw is configured in %s mode",
		strings.Join(allowed, " or "), current)
}
