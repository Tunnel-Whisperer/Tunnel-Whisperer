package cli

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/api"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/dashboard"
	"github.com/tunnelwhisperer/tw/internal/logging"
	"github.com/tunnelwhisperer/tw/internal/ops"
	"github.com/tunnelwhisperer/tw/internal/service"
)

var (
	dashboardPort  int
	runAsService   bool
)

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Start the web dashboard",
	RunE:  runDashboard,
}

func init() {
	dashboardCmd.Flags().IntVar(&dashboardPort, "port", 0, "dashboard listen port (overrides config)")
	dashboardCmd.Flags().BoolVar(&runAsService, "run-as-service", false, "run under the system service manager")
	_ = dashboardCmd.Flags().MarkHidden("run-as-service")
	rootCmd.AddCommand(dashboardCmd)
}

// slogProgress logs ProgressEvents via slog so they appear in the dashboard console.
func slogProgress(e ops.ProgressEvent) {
	switch e.Status {
	case "running":
		slog.Info(e.Label, "step", fmt.Sprintf("%d/%d", e.Step, e.Total), "status", "running")
	case "completed":
		slog.Info(e.Label, "step", fmt.Sprintf("%d/%d", e.Step, e.Total), "status", "completed")
	case "failed":
		slog.Error(e.Label, "step", fmt.Sprintf("%d/%d", e.Step, e.Total), "error", e.Error)
	}
}

func runDashboard(cmd *cobra.Command, args []string) error {
	// When running as a Windows service, redirect logs to a file
	// since the SCM discards stdout/stderr.
	if service.IsWindowsService() {
		if f, err := logging.EnableFileLog(config.Dir()); err == nil {
			defer f.Close()
			logging.Setup(logLevel, logFormat)
		}
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing ops: %w", err)
	}

	// Start gRPC API so CLI commands can talk to this daemon.
	apiAddr := fmt.Sprintf(":%d", cfg.Server.APIPort)
	apiSrv := api.NewServer(o, apiAddr)
	go func() {
		slog.Info("gRPC API listening", "addr", apiAddr)
		if err := apiSrv.Run(); err != nil {
			slog.Error("gRPC API error", "error", err)
		}
	}()

	port := cfg.Server.DashboardPort
	if dashboardPort != 0 {
		port = dashboardPort
	}

	addr := fmt.Sprintf(":%d", port)
	srv := dashboard.NewServer(addr, o)

	// Auto-start server or client if ready.
	mode := o.Mode()
	autoStart := func() {
		if mode == "server" && o.GetRelayStatus().Provisioned {
			slog.Info("auto-starting server (relay is provisioned)")
			if err := o.StartServer(slogProgress); err != nil {
				slog.Error("auto-start server failed", "error", err)
			}
		} else if mode == "client" && cfg.Xray.RelayHost != "" {
			slog.Info("auto-connecting client")
			if err := o.StartClient(slogProgress); err != nil {
				slog.Error("auto-connect client failed", "error", err)
			}
		}
	}

	shutdown := func() {
		slog.Info("shutting down...")
		_ = srv.Stop()
		apiSrv.Stop()
		if mode == "server" {
			_ = o.StopServer(nil)
		} else if mode == "client" {
			_ = o.StopClient(nil)
		}
	}

	// Windows service mode: delegate lifecycle to the SCM.
	if runAsService && service.IsWindowsService() {
		return service.RunAsService(
			func() error {
				go autoStart()
				go func() {
					if err := srv.Run(); err != nil {
						slog.Error("dashboard error", "error", err)
					}
				}()
				return nil
			},
			shutdown,
		)
	}

	// Interactive / Linux systemd mode: run HTTP server in a goroutine,
	// block on OS signals for graceful shutdown.
	go autoStart()
	go func() {
		if err := srv.Run(); err != nil {
			slog.Error("dashboard error", "error", err)
		}
	}()

	fmt.Printf("Starting dashboard on http://localhost%s\n", addr)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	fmt.Println()
	shutdown()
	return nil
}
