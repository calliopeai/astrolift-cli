package main

import (
	"os"

	"github.com/calliopeai/astrolift-cli/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
