// Command tw is the Tunnel Whisperer binary entry point. It dispatches to the
// Cobra CLI in internal/cli, which selects server or client behavior based on
// the persisted config mode.
package main

import (
	"os"

	"github.com/tunnelwhisperer/tw/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
