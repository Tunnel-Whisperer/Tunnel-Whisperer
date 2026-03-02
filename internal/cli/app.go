package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "Manage application templates",
}

var appListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all application templates",
	RunE:  runAppList,
}

var appCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an application template",
	RunE:  runAppCreate,
}

var appEditCmd = &cobra.Command{
	Use:   "edit <name>",
	Short: "Edit an application template",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppEdit,
}

var appDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete an application template",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppDelete,
}

func init() {
	appCmd.AddCommand(appListCmd)
	appCmd.AddCommand(appCreateCmd)
	appCmd.AddCommand(appEditCmd)
	appCmd.AddCommand(appDeleteCmd)
	rootCmd.AddCommand(appCmd)
}

func runAppList(cmd *cobra.Command, args []string) error {
	if err := requireMode("server"); err != nil {
		return err
	}

	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	apps := o.ListApplications()
	if len(apps) == 0 {
		fmt.Println("  No applications configured.")
		return nil
	}

	fmt.Println()
	for _, app := range apps {
		fmt.Printf("  %s (%d mapping%s)\n", app.Name, len(app.Mappings), plural(len(app.Mappings)))
		for _, m := range app.Mappings {
			fmt.Printf("    %d → %d\n", m.ClientPort, m.ServerPort)
		}
	}
	fmt.Println()
	return nil
}

func runAppCreate(cmd *cobra.Command, args []string) error {
	if err := requireMode("server"); err != nil {
		return err
	}

	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println()
	fmt.Println("=== Create Application ===")
	fmt.Println()

	fmt.Print("  Name: ")
	scanner.Scan()
	name := strings.TrimSpace(scanner.Text())
	if name == "" {
		return fmt.Errorf("application name is required")
	}
	fmt.Println()

	mappings := promptMappings(scanner)

	if err := o.CreateApplication(config.Application{Name: name, Mappings: mappings}); err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("  Application %q created.\n", name)
	fmt.Println()
	return nil
}

func runAppEdit(cmd *cobra.Command, args []string) error {
	if err := requireMode("server"); err != nil {
		return err
	}
	name := args[0]

	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	// Show current mappings.
	apps := o.ListApplications()
	var found *config.Application
	for i, a := range apps {
		if a.Name == name {
			found = &apps[i]
			break
		}
	}
	if found == nil {
		return fmt.Errorf("application %q not found", name)
	}

	fmt.Println()
	fmt.Printf("=== Edit Application: %s ===\n", name)
	fmt.Println()
	fmt.Println("  Current mappings:")
	for _, m := range found.Mappings {
		fmt.Printf("    %d → %d\n", m.ClientPort, m.ServerPort)
	}
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)

	fmt.Print("  New name (enter to keep current): ")
	scanner.Scan()
	newName := strings.TrimSpace(scanner.Text())
	if newName == "" {
		newName = name
	}
	fmt.Println()

	fmt.Println("  Enter new mappings:")
	mappings := promptMappings(scanner)

	if err := o.UpdateApplication(name, config.Application{Name: newName, Mappings: mappings}); err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("  Application %q updated.\n", newName)
	fmt.Println("  Note: this does not affect users previously created with this application.")
	fmt.Println()
	return nil
}

func runAppDelete(cmd *cobra.Command, args []string) error {
	if err := requireMode("server"); err != nil {
		return err
	}
	name := args[0]

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Printf("  Delete application %q? [y/N]: ", name)
	scanner.Scan()
	if answer := strings.TrimSpace(strings.ToLower(scanner.Text())); answer != "y" {
		fmt.Println("  Aborted.")
		return nil
	}

	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	if err := o.DeleteApplication(name); err != nil {
		return err
	}

	fmt.Printf("  Application %q deleted.\n", name)
	return nil
}

func promptMappings(scanner *bufio.Scanner) []config.PortMapping {
	fmt.Println("  Enter port mappings. Empty client port to finish.")
	fmt.Println()

	var mappings []config.PortMapping
	for i := 1; ; i++ {
		fmt.Printf("  Mapping %d:\n", i)
		fmt.Printf("    Client port: ")
		scanner.Scan()
		clientPortStr := strings.TrimSpace(scanner.Text())
		if clientPortStr == "" {
			break
		}
		clientPort, err := strconv.Atoi(clientPortStr)
		if err != nil || clientPort < 1 || clientPort > 65535 {
			fmt.Printf("    Invalid port: %s\n", clientPortStr)
			continue
		}

		fmt.Printf("    Server port: ")
		scanner.Scan()
		serverPortStr := strings.TrimSpace(scanner.Text())
		if serverPortStr == "" {
			continue
		}
		serverPort, err := strconv.Atoi(serverPortStr)
		if err != nil || serverPort < 1 || serverPort > 65535 {
			fmt.Printf("    Invalid port: %s\n", serverPortStr)
			continue
		}

		mappings = append(mappings, config.PortMapping{ClientPort: clientPort, ServerPort: serverPort})
		fmt.Printf("    → %d → %d\n", clientPort, serverPort)
		fmt.Println()
	}
	return mappings
}

func plural(n int) string {
	if n != 1 {
		return "s"
	}
	return ""
}
