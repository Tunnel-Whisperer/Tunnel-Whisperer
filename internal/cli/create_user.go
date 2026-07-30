package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var (
	createUserFrom   string
	createUserMaps   []string
	createUserSingle bool
)

var createUserCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Create a client user with tunnel access",
	Long: `Create a client user with tunnel access.

With a name argument it runs non-interactively (one command); without one it
prompts. Port mappings come from --map (repeatable) or --from.

Examples:
  tw server user create alice -m 8080:80 -m 5432:5432
  tw server user create bob --from alice`,
	Args: cobra.MaximumNArgs(1),
	RunE: runCreateUser,
}

func init() {
	createUserCmd.Flags().StringVar(&createUserFrom, "from", "", "copy port mappings from an existing user")
	createUserCmd.Flags().StringArrayVarP(&createUserMaps, "map", "m", nil,
		"port mapping clientPort:serverPort (repeatable), e.g. -m 8080:80")
	createUserCmd.Flags().BoolVar(&createUserSingle, "single-session", false, "enforce one concurrent session for this user")
	serverUserCmd.AddCommand(createUserCmd)
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

func runCreateUser(cmd *cobra.Command, args []string) error {
	if err := requireMode("server"); err != nil {
		return err
	}

	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	// Non-interactive path: name given as an argument, everything else from flags.
	//   tw server user create alice -m 8080:80 -m 5432:5432
	if len(args) == 1 {
		return createUserInline(o, strings.TrimSpace(args[0]))
	}

	return createUserInteractive(o)
}

// createUserInline creates a user in one shot from flags (no prompts).
func createUserInline(o *ops.Ops, name string) error {
	if name == "" {
		return fmt.Errorf("user name is required")
	}

	var mappings []config.PortMapping
	var err error
	switch {
	case createUserFrom != "":
		if len(createUserMaps) > 0 {
			return fmt.Errorf("use either --from or --map, not both")
		}
		mappings, err = mappingsFromUser(o, createUserFrom)
	default:
		mappings, err = parsePortMappings(createUserMaps)
	}
	if err != nil {
		return err
	}
	if len(mappings) == 0 {
		return fmt.Errorf("at least one port mapping is required (use --map clientPort:serverPort or --from <user>)")
	}

	req := ops.CreateUserRequest{Name: name, Mappings: mappings, SingleSession: createUserSingle}
	if err := o.CreateUser(context.Background(), req, cliProgress); err != nil {
		return err
	}
	fmt.Printf("  User %q created with %d port mapping(s).\n", name, len(mappings))
	return nil
}

// createUserInteractive is the prompt-driven wizard (no name argument given).
func createUserInteractive(o *ops.Ops) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println()
	fmt.Println("=== Tunnel Whisperer — Create User ===")
	fmt.Println()

	// ── Step 1: User Name ──────────────────────────────────────────────
	fmt.Println("[1/5] User name")
	fmt.Print("      Name: ")
	scanner.Scan()
	userName := strings.TrimSpace(scanner.Text())
	if userName == "" {
		return fmt.Errorf("user name is required")
	}
	fmt.Println()

	// ── Step 2: Port Mappings ──────────────────────────────────────────
	var mappings []config.PortMapping

	if createUserFrom != "" {
		fmt.Printf("[2/5] Copying port mappings from user %q\n", createUserFrom)
		var err error
		mappings, err = mappingsFromUser(o, createUserFrom)
		if err != nil {
			return err
		}
		for _, m := range mappings {
			fmt.Printf("      → localhost:%d (client) → 127.0.0.1:%d (server)\n", m.ClientPort, m.ServerPort)
		}
		fmt.Println()
	} else {
		fmt.Println("[2/5] Port mappings")
		fmt.Println("      Map client local ports to server ports (localhost only).")
		fmt.Println("      Enter mappings one at a time. Empty client port to finish.")
		fmt.Println()

		for i := 1; ; i++ {
			fmt.Printf("      Mapping %d:\n", i)
			fmt.Printf("        Client local port: ")
			scanner.Scan()
			clientPortStr := strings.TrimSpace(scanner.Text())
			if clientPortStr == "" {
				if len(mappings) == 0 {
					return fmt.Errorf("at least one port mapping is required")
				}
				break
			}
			clientPort, err := strconv.Atoi(clientPortStr)
			if err != nil || clientPort < 1 || clientPort > 65535 {
				return fmt.Errorf("invalid port: %s", clientPortStr)
			}

			fmt.Printf("        Server port:       ")
			scanner.Scan()
			serverPortStr := strings.TrimSpace(scanner.Text())
			if serverPortStr == "" {
				return fmt.Errorf("server port is required")
			}
			serverPort, err := strconv.Atoi(serverPortStr)
			if err != nil || serverPort < 1 || serverPort > 65535 {
				return fmt.Errorf("invalid port: %s", serverPortStr)
			}

			mappings = append(mappings, config.PortMapping{ClientPort: clientPort, ServerPort: serverPort})
			fmt.Printf("        → localhost:%d (client) → 127.0.0.1:%d (server)\n", clientPort, serverPort)
			fmt.Println()
		}
		fmt.Println()
	}

	req := ops.CreateUserRequest{
		Name:     userName,
		Mappings: mappings,
	}

	if err := o.CreateUser(context.Background(), req, cliProgress); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("=== User created ===")
	fmt.Println()
	fmt.Println("  Send the user's config directory to the client.")
	fmt.Println("  The client places these files in their config directory and runs `tw client connect`.")
	fmt.Println()

	return nil
}
