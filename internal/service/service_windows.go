//go:build windows

package service

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// handler implements svc.Handler for the Windows Service Control Manager.
type handler struct {
	startFn func() error
	stopFn  func()
}

func (h *handler) Execute(args []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	s <- svc.Status{State: svc.StartPending}

	if err := h.startFn(); err != nil {
		s <- svc.Status{State: svc.StopPending}
		return false, 1
	}

	s <- svc.Status{
		State:   svc.Running,
		Accepts: svc.AcceptStop | svc.AcceptShutdown,
	}

	for c := range r {
		switch c.Cmd {
		case svc.Stop, svc.Shutdown:
			s <- svc.Status{State: svc.StopPending}
			h.stopFn()
			return false, 0
		case svc.Interrogate:
			s <- c.CurrentStatus
		}
	}
	return false, 0
}

// Install registers the service with the Windows Service Control Manager.
func Install(cfg Config) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connecting to SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(cfg.Name)
	if err == nil {
		s.Close()
		return fmt.Errorf("service %q already exists", cfg.Name)
	}

	s, err = m.CreateService(cfg.Name, cfg.ExePath, mgr.Config{
		DisplayName:  cfg.DisplayName,
		Description:  cfg.Description,
		StartType:    mgr.StartAutomatic,
		ServiceStartName: "LocalSystem",
	}, cfg.Args...)
	if err != nil {
		return fmt.Errorf("creating service: %w", err)
	}
	defer s.Close()

	// Set recovery: restart after 5 seconds on first three failures.
	err = s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
	}, 86400) // reset failure count after 24h
	if err != nil {
		// Non-fatal — service is installed, just no recovery policy.
		fmt.Printf("warning: could not set recovery actions: %v\n", err)
	}

	return nil
}

// Uninstall stops and removes the service from the SCM.
func Uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connecting to SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService("tw")
	if err != nil {
		return fmt.Errorf("opening service: %w", err)
	}
	defer s.Close()

	// Try to stop first (ignore error if not running).
	_, _ = s.Control(svc.Stop)

	if err := s.Delete(); err != nil {
		return fmt.Errorf("deleting service: %w", err)
	}

	return nil
}

// Start starts the service via the SCM.
func Start() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connecting to SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService("tw")
	if err != nil {
		return fmt.Errorf("opening service: %w", err)
	}
	defer s.Close()

	return s.Start()
}

// Stop stops the service via the SCM.
func Stop() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connecting to SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService("tw")
	if err != nil {
		return fmt.Errorf("opening service: %w", err)
	}
	defer s.Close()

	_, err = s.Control(svc.Stop)
	return err
}

// IsWindowsService reports whether the process is running as a Windows service.
func IsWindowsService() bool {
	is, _ := svc.IsWindowsService()
	return is
}

// RunAsService runs the given start/stop functions under the Windows SCM.
func RunAsService(startFn func() error, stopFn func()) error {
	return svc.Run("tw", &handler{
		startFn: startFn,
		stopFn:  stopFn,
	})
}
