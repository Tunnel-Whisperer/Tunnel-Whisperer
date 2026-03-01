//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const unitPath = "/etc/systemd/system/tw.service"

const unitTemplate = `[Unit]
Description=%s
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s
Restart=on-failure
RestartSec=5
TimeoutStopSec=30

[Install]
WantedBy=multi-user.target
`

// Install writes a systemd unit file and enables the service.
func Install(cfg Config) error {
	execLine := cfg.ExePath
	if len(cfg.Args) > 0 {
		execLine += " " + strings.Join(cfg.Args, " ")
	}

	unit := fmt.Sprintf(unitTemplate, cfg.Description, execLine)

	if err := os.WriteFile(unitPath, []byte(unit), 0644); err != nil {
		return fmt.Errorf("writing unit file: %w", err)
	}

	if err := systemctl("daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}

	if err := systemctl("enable", "tw.service"); err != nil {
		return fmt.Errorf("enable: %w", err)
	}

	return nil
}

// Uninstall stops, disables, and removes the systemd unit.
func Uninstall() error {
	_ = systemctl("stop", "tw.service")
	_ = systemctl("disable", "tw.service")

	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing unit file: %w", err)
	}

	return systemctl("daemon-reload")
}

// Start starts the service via systemctl.
func Start() error {
	return systemctl("start", "tw.service")
}

// Stop stops the service via systemctl.
func Stop() error {
	return systemctl("stop", "tw.service")
}

// IsWindowsService always returns false on Linux.
func IsWindowsService() bool { return false }

// RunAsService is a no-op on Linux (systemd runs the process directly).
func RunAsService(startFn func() error, stopFn func()) error {
	return fmt.Errorf("RunAsService is only used on Windows")
}

func systemctl(args ...string) error {
	cmd := exec.Command("systemctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
