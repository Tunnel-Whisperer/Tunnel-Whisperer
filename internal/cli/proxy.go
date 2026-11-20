package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/egress"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Show or configure the outbound proxy",
	Long: `Show, set, or clear the outbound proxy.

Precedence: the proxy set here (config) wins; otherwise HTTPS_PROXY, https_proxy,
ALL_PROXY or all_proxy from the environment (first non-empty); otherwise direct.
HTTP_PROXY is ignored. NO_PROXY is honoured for the environment proxy only.
The resolved proxy covers the Xray tunnel, enrollment (tw join), cloud API
checks, relay checks, direct relay SSH, and Terraform.

Supported proxy URL formats:
  socks5://host:port
  socks5://user:pass@host:port
  http://host:port
  http://user:pass@host:port
If the port is omitted, 1080 is used (for both socks5:// and http://).
socks5h:// is accepted as an alias of socks5://; the proxy always resolves the
relay's DNS name, so the two behave identically.
In the environment variables, a value without a scheme (proxy.corp:3128) is
treated as http://; a missing port means 1080.

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
	if err := requireMode("server", "client", "relay"); err != nil {
		return err
	}
	o, err := ops.New()
	if err != nil {
		return err
	}
	p, err := o.Proxy()
	if err != nil {
		return err
	}
	fmt.Printf("  Proxy: %s\n", p)
	if p.Source == egress.SourceEnv {
		fmt.Println("  (resolved from this shell's environment; a service may differ — check the daemon log line 'outbound proxy')")
	}
	if p.Source == egress.SourceConfig {
		for _, name := range []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
			if os.Getenv(name) != "" {
				fmt.Printf("  %s is set but ignored while a proxy is configured\n", name)
				break
			}
		}
	}
	return nil
}

func runProxySet(cmd *cobra.Command, args []string) error {
	if err := requireMode("server", "client", "relay"); err != nil {
		return err
	}
	if err := requireWritableConfig(); err != nil {
		return err
	}
	o, err := ops.New()
	if err != nil {
		return err
	}
	if err := o.SetProxy(args[0]); err != nil {
		return err
	}
	u, err := egress.ParseURL(args[0])
	if err != nil {
		return err
	}
	fmt.Printf("  Proxy set to: %s\n", egress.RedactURL(u))
	fmt.Println("  (takes effect on next server/client start)")
	return nil
}

func runProxyClear(cmd *cobra.Command, args []string) error {
	if err := requireMode("server", "client", "relay"); err != nil {
		return err
	}
	if err := requireWritableConfig(); err != nil {
		return err
	}
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
