package logging

import (
	"log/slog"
	"os"
	"strings"
)

// XrayLevel holds the Xray-compatible log level string (e.g. "debug", "warning").
// Set by Setup()/SetLevel() and read by the xray package when building configs.
var XrayLevel = "warning"

// level is a dynamic level variable shared by all handlers in the chain.
// Changing it via SetLevel() takes effect immediately without replacing
// the handler (important for the dashboard's tee handler wrapper).
var level slog.LevelVar

// Setup initializes the default slog logger at the given level.
// Valid levels: "debug", "info", "warn", "error". Defaults to "info".
func Setup(lvl string) {
	applyLevel(lvl)
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: &level})
	slog.SetDefault(slog.New(h))
}

// SetLevel changes the log level at runtime without replacing the handler.
// This is safe to call while the dashboard tee handler is active.
func SetLevel(lvl string) {
	applyLevel(lvl)
}

func applyLevel(lvl string) {
	switch strings.ToLower(lvl) {
	case "debug":
		level.Set(slog.LevelDebug)
		XrayLevel = "debug"
	case "warn", "warning":
		level.Set(slog.LevelWarn)
		XrayLevel = "warning"
	case "error":
		level.Set(slog.LevelError)
		XrayLevel = "error"
	default:
		level.Set(slog.LevelInfo)
		XrayLevel = "warning"
	}
}
