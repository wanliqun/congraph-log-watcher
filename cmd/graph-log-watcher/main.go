// Command graph-log-watcher watches graph-node container logs and emits
// actionable incident alerts.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/wanliqun/congraph-log-watcher/internal/config"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "check-config":
		return runCheckConfig(args[1:], stdout, stderr)
	case "run":
		fmt.Fprintln(stderr, "run is not available until the runtime service is implemented")
		return 2
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runCheckConfig(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("check-config", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "path to the YAML configuration file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *path == "" {
		fmt.Fprintln(stderr, "--config is required")
		return 2
	}

	if _, err := config.Load(*path); err != nil {
		fmt.Fprintf(stderr, "invalid configuration: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, "configuration is valid")
	return 0
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: graph-log-watcher check-config --config <path>")
}
