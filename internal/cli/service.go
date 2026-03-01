package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/service"
)

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage system service (Linux systemd / Windows SCM)",
}

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install tw as a system service",
	RunE: func(cmd *cobra.Command, args []string) error {
		exePath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolving executable path: %w", err)
		}

		svcArgs := []string{"dashboard", "--run-as-service"}

		err = service.Install(service.Config{
			Name:        "tw",
			DisplayName: "Tunnel Whisperer",
			Description: "Tunnel Whisperer Dashboard Service",
			ExePath:     exePath,
			Args:        svcArgs,
		})
		if err != nil {
			return err
		}

		fmt.Println("Service installed successfully.")
		fmt.Println("Start with: tw service start")
		return nil
	},
}

var serviceUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall the system service",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := service.Uninstall(); err != nil {
			return err
		}
		fmt.Println("Service uninstalled successfully.")
		return nil
	},
}

var serviceStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the system service",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := service.Start(); err != nil {
			return err
		}
		fmt.Println("Service started.")
		return nil
	},
}

var serviceStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the system service",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := service.Stop(); err != nil {
			return err
		}
		fmt.Println("Service stopped.")
		return nil
	},
}

func init() {
	serviceCmd.AddCommand(serviceInstallCmd)
	serviceCmd.AddCommand(serviceUninstallCmd)
	serviceCmd.AddCommand(serviceStartCmd)
	serviceCmd.AddCommand(serviceStopCmd)
	rootCmd.AddCommand(serviceCmd)
}
