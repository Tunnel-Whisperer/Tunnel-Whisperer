package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Show or configure the outbound proxy",
	Long: `Show, set, or clear the proxy used for all outbound connections.

Supported proxy URL formats:
  socks5://host:port
  socks5://user:pass@host:port
  http://host:port
  http://user:pass@host:port

Examples:
  tw proxy                              Show current proxy
  tw proxy set socks5://proxy:1080      Set SOCKS5 proxy
  tw proxy set http://user:pass@p:8080  Set HTTP proxy with auth
  tw proxy clear                        Remove proxy`,
	RunE: runProxyShow,
}

var proxySetCmd = &cobra.Command{
	Use:   "set <url>",
	Short: "Set the outbound proxy URL",
	Args:  cobra.ExactArgs(1),
	RunE:  runProxySet,
}

var proxyClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Remove the outbound proxy",
	RunE:  runProxyClear,
}

func init() {
	proxyCmd.AddCommand(proxySetCmd)
	proxyCmd.AddCommand(proxyClearCmd)
	rootCmd.AddCommand(proxyCmd)
}

func runProxyShow(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Proxy == "" {
		fmt.Println("  Proxy: not configured")
	} else {
		fmt.Printf("  Proxy: %s\n", cfg.Proxy)
	}
	return nil
}

func runProxySet(cmd *cobra.Command, args []string) error {
	o, err := ops.New()
	if err != nil {
		return err
	}
	if err := o.SetProxy(args[0]); err != nil {
		return err
	}
	fmt.Printf("  Proxy set to: %s\n", args[0])
	fmt.Println("  (takes effect on next server/client start)")
	return nil
}

func runProxyClear(cmd *cobra.Command, args []string) error {
	o, err := ops.New()
	if err != nil {
		return err
	}
	if err := o.SetProxy(""); err != nil {
		return err
	}
	fmt.Println("  Proxy cleared")
	return nil
}
