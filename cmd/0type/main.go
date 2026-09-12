// Command 0type is a dead-simple live voice-to-text overlay for Linux/Wayland.
package main

import (
	"fmt"
	"os"
	"strings"
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
		return runApp(nil)
	}

	switch args[0] {
	case "version":
		fmt.Println("0type " + version)
		return nil
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	case "debug":
		return runDebug(args[1:])
	case "setup":
		return runSetup(args[1:])
	case "listen":
		return runListen(args[1:])
	case "ui":
		return runUI(args[1:])
	case "toggle":
		return runToggle(args[1:])
	case "themes":
		return runThemes(args[1:])
	case "config":
		return runConfig(args[1:])
	default:
		// Flags with no subcommand (`0type --theme light`) are the normal
		// entry point, not a mistake.
		if strings.HasPrefix(args[0], "-") {
			return runApp(args)
		}
		return fmt.Errorf("unknown subcommand %q (try `0type help`)", args[0])
	}
}

const usage = `0type -- live voice-to-text overlay

Usage:
  0type [--theme NAME]   open the menu (starts 0type if it isn't running)
  0type --background     start hidden and stay resident (for autostart)
  0type toggle           show or hide the running overlay; bind this to a key
  0type setup            download the speech model (~650MB, once)

  0type themes           list available themes
  0type config           show the resolved configuration and where it came from
  0type ui [--theme X]   preview the overlay without the model or microphone
  0type listen           print live transcription to stdout, no window
  0type version          print the version
`
