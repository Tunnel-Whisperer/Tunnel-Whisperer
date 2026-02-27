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
