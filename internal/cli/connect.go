package cli

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var connectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Connect to a relay as a client",
	RunE:  runConnect,
}

var connectMaps []string

func init() {
	clientCmd.AddCommand(connectCmd)
	connectCmd.Flags().StringArrayVar(&connectMaps, "map", nil,
		"one-shot local-port override, <local_port>:<server_port> (repeatable, not persisted)")
}

func runConnect(cmd *cobra.Command, args []string) error {
	if err := requireMode("client"); err != nil {
		return err
	}
	fmt.Println("Connecting to relay...")

	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	fmt.Printf("Config: %s\n", config.FilePath())

	overrides, err := parseMapFlags(connectMaps)
	if err != nil {
		return err
	}

	if err := o.StartClient(cliProgress, overrides); err != nil {
		return err
	}

	fmt.Println("Client connected. Press Ctrl-C to stop.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	fmt.Println("\nDisconnecting...")
	o.StopClient(nil)
	return nil
}

// parseMapFlags parses repeated --map values of the form
// "<local_port>:<server_port>" (ssh -L ordering) into a
// server-port → local-port map.
func parseMapFlags(vals []string) (map[int]int, error) {
	if len(vals) == 0 {
		return nil, nil
	}
	out := make(map[int]int, len(vals))
	for _, v := range vals {
		lp, sp, ok := strings.Cut(v, ":")
		if !ok {
			return nil, fmt.Errorf("invalid --map %q: want <local_port>:<server_port>", v)
		}
		local, err := strconv.Atoi(lp)
		if err != nil {
			return nil, fmt.Errorf("invalid --map %q: local port %q is not a number", v, lp)
		}
		server, err := strconv.Atoi(sp)
		if err != nil {
			return nil, fmt.Errorf("invalid --map %q: server port %q is not a number", v, sp)
		}
		if prev, dup := out[server]; dup {
			return nil, fmt.Errorf("--map: server port %d mapped twice (%d and %d)", server, prev, local)
		}
		out[server] = local
	}
	return out, nil
}
