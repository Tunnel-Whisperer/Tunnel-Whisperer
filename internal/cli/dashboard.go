package cli

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/api"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/dashboard"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var dashboardPort int

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Start the web dashboard",
	RunE:  runDashboard,
}

func init() {
	dashboardCmd.Flags().IntVar(&dashboardPort, "port", 0, "dashboard listen port (overrides config)")
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
	fmt.Printf("Starting dashboard on http://localhost%s\n", addr)
	srv := dashboard.NewServer(addr, o)

	// Auto-start server or client if ready.
	mode := o.Mode()
	if mode == "server" && o.GetRelayStatus().Provisioned {
		go func() {
			slog.Info("auto-starting server (relay is provisioned)")
			if err := o.StartServer(slogProgress); err != nil {
				slog.Error("auto-start server failed", "error", err)
			}
		}()
	} else if mode == "client" && cfg.Xray.RelayHost != "" {
		go func() {
			slog.Info("auto-connecting client")
			if err := o.StartClient(slogProgress); err != nil {
				slog.Error("auto-connect client failed", "error", err)
			}
		}()
	}

	return srv.Run()
}
