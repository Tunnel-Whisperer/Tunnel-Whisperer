package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var (
	inviteUserFrom   string
	inviteUserMaps   []string
	inviteUserSingle bool
	inviteUserTTL    time.Duration
)

var inviteUserCmd = &cobra.Command{
	Use:   "invite [name]",
	Short: "Enroll a client user over a one-time code (no files)",
	Long: `Enroll a client user over a one-time invite code.

Mints a code carrying the user's port mappings and waits for the enrollee.
Read the code to them over any channel; they run:

  tw join <relay-host> <code>

When their enrollment arrives, BOTH terminals show a short authentication
string. Approve only on an exact match. The client generates its own keys
locally — no private material ever leaves their machine, and no files
change hands.

With a name argument the mappings come from --map (repeatable) or --from;
without one it prompts for both. Re-inviting an existing name is refused:
to change a user's device or mappings, delete the user and invite again.

Examples:
  tw server user invite alice -m 8080:80 -m 5432:5432
  tw server user invite bob --from alice`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInviteUser,
}

func init() {
	inviteUserCmd.Flags().StringVar(&inviteUserFrom, "from", "", "copy port mappings from an existing user")
	inviteUserCmd.Flags().StringArrayVarP(&inviteUserMaps, "map", "m", nil,
		"port mapping clientPort:serverPort (repeatable), e.g. -m 8080:80")
	inviteUserCmd.Flags().BoolVar(&inviteUserSingle, "single-session", false, "enforce one concurrent session for this user")
	inviteUserCmd.Flags().DurationVar(&inviteUserTTL, "ttl", 15*time.Minute, "invite lifetime")
	serverUserCmd.AddCommand(inviteUserCmd)
}

// parsePortMappings parses "clientPort:serverPort" specs into PortMappings.
func parsePortMappings(specs []string) ([]config.PortMapping, error) {
	var out []config.PortMapping
	for _, s := range specs {
		parts := strings.SplitN(strings.TrimSpace(s), ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid --map %q: want clientPort:serverPort", s)
		}
		cp, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || cp < 1 || cp > 65535 {
			return nil, fmt.Errorf("invalid client port in --map %q", s)
		}
		sp, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || sp < 1 || sp > 65535 {
			return nil, fmt.Errorf("invalid server port in --map %q", s)
		}
		out = append(out, config.PortMapping{ClientPort: cp, ServerPort: sp})
	}
	return out, nil
}

// mappingsFromUser copies the port mappings of an existing user.
func mappingsFromUser(o *ops.Ops, from string) ([]config.PortMapping, error) {
	users, err := o.ListUsers()
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}
	for _, u := range users {
		if u.Name == from {
			var mappings []config.PortMapping
			for _, t := range u.Tunnels {
				mappings = append(mappings, config.PortMapping{ClientPort: t.LocalPort, ServerPort: t.RemotePort})
			}
			if len(mappings) == 0 {
				return nil, fmt.Errorf("user %q has no port mappings", from)
			}
			return mappings, nil
		}
	}
	return nil, fmt.Errorf("user %q not found", from)
}

func runInviteUser(cmd *cobra.Command, args []string) error {
	if err := requireMode("server"); err != nil {
		return err
	}
	if err := requireWritableConfig(); err != nil {
		return err
	}

	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	var name string
	var mappings []config.PortMapping
	if len(args) == 1 {
		name = strings.TrimSpace(args[0])
		if name == "" {
			return fmt.Errorf("user name is required")
		}
		switch {
		case inviteUserFrom != "":
			if len(inviteUserMaps) > 0 {
				return fmt.Errorf("use either --from or --map, not both")
			}
			mappings, err = mappingsFromUser(o, inviteUserFrom)
		default:
			mappings, err = parsePortMappings(inviteUserMaps)
		}
		if err != nil {
			return err
		}
		if len(mappings) == 0 {
			return fmt.Errorf("at least one port mapping is required (use --map clientPort:serverPort or --from <user>)")
		}
	} else {
		name, mappings, err = promptInviteUser(o)
		if err != nil {
			return err
		}
	}

	req := ops.CreateUserRequest{Name: name, Mappings: mappings, SingleSession: inviteUserSingle}
	if err := o.InviteUser(req, inviteUserTTL, ops.InviteUI{
		ShowCode: func(code string, expires time.Time) {
			fmt.Printf("Invite code: %s\n", code)
			fmt.Printf("Expires:     %s\n", expires.Format(time.Kitchen))
			fmt.Printf("\nRun this on %s's client machine:\n", req.Name)
			fmt.Printf("\n  tw join %s %s\n\n", o.Config().Xray.RelayHost, code)
			fmt.Println("Waiting for the enrollee ...")
		},
		ConfirmSAS: func(sas string) bool {
			fmt.Printf("\nSAS: %s\n", sas)
			fmt.Print("Does the enrollee read back EXACTLY this string? [y/N]: ")
			line, _ := sharedLine()
			return line == "y" || line == "Y" || line == "yes"
		},
	}, cliProgress); err != nil {
		return err
	}
	fmt.Printf("User %q enrolled (client context delivered).\n", req.Name)
	return nil
}

// promptInviteUser is the lean no-arg wizard: name + mappings, then the
// same ceremony as the flag form.
func promptInviteUser(o *ops.Ops) (string, []config.PortMapping, error) {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println()
	fmt.Println("=== Tunnel Whisperer — Invite User ===")
	fmt.Println()

	fmt.Println("[1/2] User name")
	fmt.Print("      Name: ")
	scanner.Scan()
	name := strings.TrimSpace(scanner.Text())
	if name == "" {
		return "", nil, fmt.Errorf("user name is required")
	}
	fmt.Println()

	if inviteUserFrom != "" {
		fmt.Printf("[2/2] Copying port mappings from user %q\n", inviteUserFrom)
		mappings, err := mappingsFromUser(o, inviteUserFrom)
		if err != nil {
			return "", nil, err
		}
		for _, m := range mappings {
			fmt.Printf("      → localhost:%d (client) → 127.0.0.1:%d (server)\n", m.ClientPort, m.ServerPort)
		}
		fmt.Println()
		return name, mappings, nil
	}

	fmt.Println("[2/2] Port mappings")
	fmt.Println("      Map client local ports to server ports (localhost only).")
	fmt.Println("      Enter mappings one at a time. Empty client port to finish.")
	fmt.Println()

	var mappings []config.PortMapping
	for i := 1; ; i++ {
		fmt.Printf("      Mapping %d:\n", i)
		fmt.Printf("        Client local port: ")
		scanner.Scan()
		clientPortStr := strings.TrimSpace(scanner.Text())
		if clientPortStr == "" {
			if len(mappings) == 0 {
				return "", nil, fmt.Errorf("at least one port mapping is required")
			}
			break
		}
		clientPort, err := strconv.Atoi(clientPortStr)
		if err != nil || clientPort < 1 || clientPort > 65535 {
			return "", nil, fmt.Errorf("invalid port: %s", clientPortStr)
		}

		fmt.Printf("        Server port:       ")
		scanner.Scan()
		serverPortStr := strings.TrimSpace(scanner.Text())
		if serverPortStr == "" {
			return "", nil, fmt.Errorf("server port is required")
		}
		serverPort, err := strconv.Atoi(serverPortStr)
		if err != nil || serverPort < 1 || serverPort > 65535 {
			return "", nil, fmt.Errorf("invalid port: %s", serverPortStr)
		}

		mappings = append(mappings, config.PortMapping{ClientPort: clientPort, ServerPort: serverPort})
		fmt.Printf("        → localhost:%d (client) → 127.0.0.1:%d (server)\n", clientPort, serverPort)
		fmt.Println()
	}
	fmt.Println()
	return name, mappings, nil
}
