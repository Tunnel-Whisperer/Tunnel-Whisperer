// Package cli defines the Cobra command tree for the tw binary, with one file
// per command. The commands are thin front-ends that call into internal/ops,
// and many are interactive wizards. root.go also enforces the server/client
// mode gate via requireMode.
package cli

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/logging"
	"github.com/tunnelwhisperer/tw/internal/ops"
	"github.com/tunnelwhisperer/tw/internal/ops/modeauth"
	"github.com/tunnelwhisperer/tw/internal/version"
)

var logLevel string
var logFormat string
var configDir string

var rootCmd = &cobra.Command{
	Use:     "tw",
	Short:   "Tunnel Whisperer — surgical, resilient connectivity",
	Version: version.Version,
	Long: `Tunnel Whisperer creates resilient, application-layer bridges for specific
ports across separated private networks. It encapsulates traffic in standard
HTTPS/WebSocket to traverse strict firewalls and DPI.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// --config-dir is the flag form of the TW_CONFIG_DIR env var: it sets the
		// same variable (flag wins over an inherited env value) so there's one
		// source of truth. Must be applied before any config.Load() below, since
		// config.Dir reads the env.
		if configDir != "" {
			d := filepath.Clean(configDir)
			os.Setenv("TW_CONFIG_DIR", d)
			_ = os.MkdirAll(d, 0o755)
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
	rootCmd.PersistentFlags().StringVar(&configDir, "config-dir", "", "config/state directory to use instead of the system default; flag form of TW_CONFIG_DIR (no permissions needed)")
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
	if err := modeError(cfg.Mode, allowed); err != nil {
		return err
	}
	return verifyModeAuth(cfg)
}

// verifyModeAuth enforces the tamper-evidence signature over cfg.Mode. An
// unset mode is the bootstrap state and needs no signature. A present but
// invalid signature is refused. A missing signature on a set mode is
// legacy-tolerated with a one-time warning — except the relay, which holds
// its own signing key and self-heals by re-signing. See internal/ops/modeauth:
// this is tamper-evidence, not a security wall.
func verifyModeAuth(cfg *config.Config) error {
	if cfg.Mode == "" {
		return nil // bootstrap: no mode set yet
	}
	if cfg.ModeAuth != nil {
		// A present signature must verify. If the identity is missing we
		// cannot verify it — fail closed rather than letting a deleted
		// id_ed25519.pub bypass a present (possibly tampered) signature.
		id, err := ops.ProfileIdentity()
		if err != nil {
			return fmt.Errorf("mode signature present but profile identity is unreadable; the profile is inconsistent")
		}
		if err := modeauth.Verify(cfg.Mode, id, cfg.ModeAuth.Sig, cfg.ModeAuth.Issuer); err != nil {
			return fmt.Errorf("mode signature invalid — the 'mode' field was modified or the profile is inconsistent; re-enroll (server) / re-import (client) / re-create (relay)")
		}
		return nil
	}
	// mode_auth ABSENT (legacy / pre-setup):
	if cfg.Mode == "relay" {
		slog.Warn("relay profile unsigned; self-signing mode_auth", "mode", cfg.Mode)
		if o, err := ops.New(); err == nil {
			_ = o.StampAndSaveModeAuth() // best-effort self-heal
		}
		return nil
	}
	slog.Warn("mode is unsigned; re-enroll (server) or re-import (client) to sign it", "mode", cfg.Mode)
	return nil
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
