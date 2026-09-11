// Command 0type is a dead-simple live voice-to-text overlay for Linux/Wayland.
package main

import (
	"fmt"
	"os"
)

// version is set at build time via -ldflags (see Makefile). Defaults to
// "dev" for local/unreleased builds.
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "0type: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no subcommand yet implemented (version %s) - see docs/SETUP.md", version)
	}

	switch args[0] {
	case "version":
		fmt.Println("0type " + version)
		return nil
	case "debug":
		return runDebug(args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}
