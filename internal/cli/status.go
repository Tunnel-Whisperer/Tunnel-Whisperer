package cli

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/api"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

// statusCmd is the top-level, ungated status: it detects the active context's
// mode and prints the same unified view the role-scoped variants show. No
// requireMode gate — this is the "what is going on here?" entry point that
// must work on any machine, set up or not.
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show overall status: active context, mode, and its live status",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sharedStatus()
	},
}

// relayStatusCmd is the relay-mode status command; gated to relay.
var relayStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current relay and tunnel status",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireMode("relay"); err != nil {
			return err
		}
		return sharedStatus()
	},
}

// serverStatusCmd is the server-mode status command; gated to server.
var serverStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current relay and tunnel status",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireMode("server"); err != nil {
			return err
		}
		return sharedStatus()
	},
}

// clientStatusCmd is the client-mode status command; gated to client.
var clientStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current relay and tunnel status",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireMode("client"); err != nil {
			return err
		}
		return sharedStatus()
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
	relayCmd.AddCommand(relayStatusCmd)
	serverCmd.AddCommand(serverStatusCmd)
	clientCmd.AddCommand(clientStatusCmd)
}

// sharedStatus contains the shared status logic used by the ungated top-level
// command and all three role variants.
func sharedStatus() error {
	printStatusHeader()
	cfg, _ := config.Load()
	addr := fmt.Sprintf("localhost:%d", cfg.Server.APIPort)

	client, err := api.Dial(addr)
	if err != nil {
		return runStatusLocal()
	}
	defer client.Close()
	return runStatusRemote(client)
}

// printStatusHeader prints the context identity header. Context data is local
// state, so it is read from disk even when the daemon answers the rest.
// Best-effort: a half-initialized profile still gets a header.
func printStatusHeader() {
	cfg, _ := config.Load()
	mode := ""
	if cfg != nil {
		mode = cfg.Mode
	}
	var cur *ops.ContextInfo
	total := 0
	if o, err := ops.New(); err == nil {
		if list, lerr := o.ListContexts(); lerr == nil {
			total = len(list)
			for i := range list {
				if list[i].Current {
					cur = &list[i]
					break
				}
			}
		}
	}
	for _, l := range statusHeaderLines(cur, total, mode, config.FilePath()) {
		fmt.Println(l)
	}
	fmt.Println()
}

func runStatusRemote(client *api.Client) error {
	resp, err := client.GetStatus(context.Background())
	if err != nil {
		return fmt.Errorf("getting status: %w", err)
	}

	if w := daemonContextMismatch(resp.Mode, resp.Relay.Domain); w != "" {
		fmt.Println(w)
		fmt.Println()
	}

	if resp.Server != nil {
		fmt.Printf("  Users:  %d (%d connected)\n", resp.UserCount, resp.ConnectedUsers)
	} else {
		fmt.Printf("  Users:  %d\n", resp.UserCount)
	}
	fmt.Println()

	printRelaySection(resp.Relay.Provisioned, resp.Relay.IP, resp.Relay.Provider, resp.Relay.Domain)

	if resp.Server != nil {
		fmt.Println()
		fmt.Println("  Server:")
		fmt.Printf("    State:   %s\n", resp.Server.State)
		fmt.Printf("    SSH:     %s\n", workingStr(resp.Server.SSH))
		fmt.Printf("    Xray:    %s\n", workingStr(resp.Server.Xray))
		fmt.Printf("    Tunnel:  %s\n", workingStr(resp.Server.Tunnel))
		if resp.Server.TunnelError != "" {
			fmt.Printf("    Error:   %s\n", resp.Server.TunnelError)
		}
	}

	if resp.Client != nil {
		fmt.Println()
		fmt.Println("  Client:")
		fmt.Printf("    State:   %s\n", resp.Client.State)
		fmt.Printf("    Xray:    %s\n", workingStr(resp.Client.Xray))
		fmt.Printf("    Tunnel:  %s\n", workingStr(resp.Client.Tunnel))
		if resp.Client.TunnelError != "" {
			fmt.Printf("    Error:   %s\n", resp.Client.TunnelError)
		}
	}

	return nil
}

func runStatusLocal() error {
	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	mode := o.Mode()
	if mode == "" {
		fmt.Println("  (not set up yet — create a relay, join one as a server, or import a client bundle)")
		return nil
	}
	relay := o.GetRelayStatus()
	users, _ := o.ListUsers()

	if mode == "server" {
		// No daemon answered, so the server is not running: nobody can be
		// connected. GetOnlineUsers agrees (nil when the Xray tunnel is down).
		fmt.Printf("  Users:  %d (0 connected)\n", len(users))
	} else {
		fmt.Printf("  Users:  %d\n", len(users))
	}
	fmt.Println()

	printRelaySection(relay.Provisioned, relay.IP, relay.Provider, relay.Domain)

	if mode == "server" || mode == "client" {
		fmt.Println()
		fmt.Println("  (daemon not running — start with `tw server start` or `tw dashboard`)")
	}

	return nil
}

// daemonContextMismatch returns a warning when a running tw service's mode or
// relay differs from the active on-disk context, or "" when they agree. The
// service keeps serving the config it loaded at startup, so a `tw config
// use-context` switch does not reach the daemon until it is restarted; this
// surfaces that drift (the source of the "I'm in context X but status shows Y"
// confusion) instead of letting it pass silently.
func daemonContextMismatch(daemonMode, daemonRelay string) string {
	cfg, err := config.Load()
	if err != nil {
		return ""
	}
	var diffs []string
	if cfg.Mode != "" && daemonMode != "" && cfg.Mode != daemonMode {
		diffs = append(diffs, fmt.Sprintf("mode    — active context: %s, running service: %s", cfg.Mode, daemonMode))
	}
	if cfg.Xray.RelayHost != "" && daemonRelay != "" && cfg.Xray.RelayHost != daemonRelay {
		diffs = append(diffs, fmt.Sprintf("relay   — active context: %s, running service: %s", cfg.Xray.RelayHost, daemonRelay))
	}
	if len(diffs) == 0 {
		return ""
	}
	return "  ⚠ The running tw service does not match the active context:\n    " +
		strings.Join(diffs, "\n    ") +
		"\n    Restart the service to apply the switch (Windows: Restart-Service tw; otherwise: tw service stop && tw service start)."
}

// printRelaySection prints the relay block shared by the remote and local paths.
func printRelaySection(provisioned bool, ip, provider, domain string) {
	fmt.Println("  Relay:")
	fmt.Printf("    Provisioned: %s\n", yesNo(provisioned))
	if provisioned {
		fmt.Printf("    IP:          %s\n", relayIPDisplay(ip, domain))
		fmt.Printf("    Provider:    %s\n", provider)
	}
}

// relayIPDisplay returns the stored relay IP, or resolves the relay domain
// when no IP is on record (joined servers and marker-less manual relays store
// none). Returns a dash when neither is available.
func relayIPDisplay(ip, domain string) string {
	if ip != "" {
		return ip
	}
	if domain == "" {
		return "—"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, domain)
	if err != nil || len(addrs) == 0 {
		return "—"
	}
	return addrs[0] + " (resolved)"
}

// workingStr renders a component's health as words — status output is read by
// humans, not parsers.
func workingStr(b bool) string {
	if b {
		return "working"
	}
	return "not working"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// statusHeaderLines renders the identity header shown by every status command:
// which context is active, its mode, user, relay, and the config file path.
// cur is nil when no context exists yet (machine not set up). Pure function so
// the layout is unit-testable without a daemon or config dir.
func statusHeaderLines(cur *ops.ContextInfo, total int, mode, configPath string) []string {
	var lines []string
	if cur == nil {
		lines = append(lines, "  Context:  — (not set up yet)")
	} else {
		lines = append(lines, fmt.Sprintf("  Context:  %s (%s) — %d stored", cur.Name, orDash(cur.ID), total))
	}
	lines = append(lines, fmt.Sprintf("  Mode:     %s", orDash(mode)))
	if cur != nil && cur.User != "" {
		lines = append(lines, fmt.Sprintf("  User:     %s", cur.User))
	}
	if cur != nil && cur.Relay != "" {
		lines = append(lines, fmt.Sprintf("  Relay:    %s", cur.Relay))
	}
	lines = append(lines, fmt.Sprintf("  Config:   %s", configPath))
	return lines
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
