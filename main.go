package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/calliopeai/astrolift-cli/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		var reported interface{ AlreadyReported() bool }
		if !errors.As(err, &reported) || !reported.AlreadyReported() {
			fmt.Fprintln(os.Stderr, "Error:", err)
		}
		os.Exit(1)
	}
}
