// Package service installs and runs tw as a native OS service, with
// platform-specific backends for systemd, the Windows SCM, and launchd
// selected by build tags.
package service

// Config holds service registration parameters.
type Config struct {
	Name        string   // service name, e.g. "tw"
	DisplayName string   // human-readable name, e.g. "Tunnel Whisperer"
	Description string   // service description
	ExePath     string   // absolute path to the executable
	Args        []string // arguments passed to the executable
}
