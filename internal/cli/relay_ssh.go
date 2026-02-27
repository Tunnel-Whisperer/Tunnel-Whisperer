package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/ops"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

var relayCmd = &cobra.Command{
	Use:   "relay",
	Short: "Relay server operations",
}

var relaySSHCmd = &cobra.Command{
	Use:   "ssh",
	Short: "Open an interactive SSH shell on the relay server",
	RunE:  runRelaySSH,
}

func init() {
	relayCmd.AddCommand(relaySSHCmd)
	rootCmd.AddCommand(relayCmd)
}

func runRelaySSH(cmd *cobra.Command, args []string) error {
	if err := requireMode("server"); err != nil {
		return err
	}
	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	status := o.GetRelayStatus()
	if !status.Provisioned {
		return fmt.Errorf("no relay provisioned — run `tw create relay-server` first")
	}

	fmt.Printf("  Connecting to relay (%s)...\n", status.Domain)

	return o.RelaySSH(func(client *gossh.Client) error {
		session, err := client.NewSession()
		if err != nil {
			return fmt.Errorf("creating session: %w", err)
		}
		defer session.Close()

		fd := int(os.Stdin.Fd())
		cols, rows, err := term.GetSize(fd)
		if err != nil {
			cols, rows = 80, 24
		}

		if err := session.RequestPty("xterm-256color", rows, cols, gossh.TerminalModes{
			gossh.ECHO:          1,
			gossh.TTY_OP_ISPEED: 14400,
			gossh.TTY_OP_OSPEED: 14400,
		}); err != nil {
			return fmt.Errorf("requesting PTY: %w", err)
		}

		oldState, err := term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("setting raw terminal: %w", err)
		}
		defer term.Restore(fd, oldState)

		session.Stdin = os.Stdin
		session.Stdout = os.Stdout
		session.Stderr = os.Stderr

		watchTermResize(fd, session)

		if err := session.Shell(); err != nil {
			return fmt.Errorf("starting shell: %w", err)
		}

		return session.Wait()
	})
}
