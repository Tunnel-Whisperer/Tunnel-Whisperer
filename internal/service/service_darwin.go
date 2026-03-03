//go:build darwin

package service

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const plistPath = "/Library/LaunchDaemons/com.tunnelwhisperer.tw.plist"

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.tunnelwhisperer.tw</string>
    <key>ProgramArguments</key>
    <array>
%s
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/var/log/tw.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/tw.err.log</string>
</dict>
</plist>
`

// Install writes a launchd plist and loads the service.
func Install(cfg Config) error {
	args := []string{cfg.ExePath}
	args = append(args, cfg.Args...)

	var argLines []string
	for _, a := range args {
		argLines = append(argLines, fmt.Sprintf("        <string>%s</string>", a))
	}

	plist := fmt.Sprintf(plistTemplate, strings.Join(argLines, "\n"))

	if err := os.WriteFile(plistPath, []byte(plist), 0644); err != nil {
		return fmt.Errorf("writing plist: %w", err)
	}

	return nil
}

// Uninstall unloads and removes the launchd plist.
func Uninstall() error {
	_ = launchctl("unload", plistPath)

	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing plist: %w", err)
	}

	return nil
}

// Start loads the service via launchctl.
func Start() error {
	return launchctl("load", plistPath)
}

// Stop unloads the service via launchctl.
func Stop() error {
	return launchctl("unload", plistPath)
}

// IsWindowsService always returns false on macOS.
func IsWindowsService() bool { return false }

// RunAsService is a no-op on macOS (launchd runs the process directly).
func RunAsService(startFn func() error, stopFn func()) error {
	return fmt.Errorf("RunAsService is only used on Windows")
}

func launchctl(args ...string) error {
	cmd := exec.Command("launchctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
