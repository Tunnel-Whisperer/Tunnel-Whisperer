package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tunnelwhisperer/tw/internal/version"
)

// XrayLevel holds the Xray-compatible log level string (e.g. "debug", "warning").
// Set by Setup()/SetLevel() and read by the xray package when building configs.
var XrayLevel = "warning"

// Format stores the active log format ("text" or "json").
var Format = "text"

// output is the destination for log output. Defaults to stderr.
// Changed by EnableFileLog to redirect to a file.
var output io.Writer = os.Stderr

// level is a dynamic level variable shared by all handlers in the chain.
// Changing it via SetLevel() takes effect immediately without replacing
// the handler (important for the dashboard's tee handler wrapper).
var level slog.LevelVar

// otelAttrMap maps application-level attribute names to OTel semantic
// convention names. Applied only in JSON mode via replaceAttr.
var otelAttrMap = map[string]string{
	"error":      "exception.message",
	"user":       "enduser.id",
	"tw_user":    "enduser.id",
	"remote":     "net.peer.name",
	"addr":       "net.host.name",
	"local_port": "net.host.port",
	"port":       "net.host.port",
	"domain":     "server.address",
}

// Setup initializes the default slog logger at the given level and format.
// Valid levels: "debug", "info", "warn", "error". Defaults to "info".
// Valid formats: "text", "json". Defaults to "text".
func Setup(lvl, format string) {
	applyLevel(lvl)
	Format = strings.ToLower(format)

	opts := &slog.HandlerOptions{
		Level:       &level,
		ReplaceAttr: replaceAttr,
	}

	var h slog.Handler
	if Format == "json" {
		h = slog.NewJSONHandler(output, opts)
		// Attach OTel resource attributes — appear on every log record.
		h = h.WithAttrs([]slog.Attr{
			slog.String("service.name", "tunnel-whisperer"),
			slog.String("service.version", version.Version),
		})
	} else {
		Format = "text"
		h = slog.NewTextHandler(output, opts)
	}

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

// replaceAttr customises slog output for both text and JSON modes.
func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if Format == "json" {
		switch a.Key {
		case slog.TimeKey:
			a.Value = slog.StringValue(a.Value.Time().Format(time.RFC3339Nano))
		case slog.LevelKey:
			lvl := a.Value.Any().(slog.Level)
			a.Key = "severity"
			a.Value = slog.StringValue(otelSeverity(lvl))
		case slog.MessageKey:
			a.Key = "body"
		}
		// Rename application attributes to OTel semantic conventions.
		if len(groups) == 0 {
			if mapped, ok := otelAttrMap[a.Key]; ok {
				a.Key = mapped
			}
		}
	} else {
		if a.Key == slog.TimeKey {
			a.Value = slog.StringValue(a.Value.Time().Format(time.DateTime))
		}
	}
	return a
}

// EnableFileLog opens a log file (tw.log) in the given directory and
// redirects log output to it. Call Setup() again after this to rebuild
// the handler with the new output. Returns the file so the caller can
// defer Close().
func EnableFileLog(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating log directory: %w", err)
	}
	path := filepath.Join(dir, "tw.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("opening log file %s: %w", path, err)
	}
	output = f
	return f, nil
}

// otelSeverity maps slog levels to OTel severity text.
func otelSeverity(l slog.Level) string {
	switch {
	case l < slog.LevelInfo:
		return "DEBUG"
	case l < slog.LevelWarn:
		return "INFO"
	case l < slog.LevelError:
		return "WARN"
	default:
		return "ERROR"
	}
}
