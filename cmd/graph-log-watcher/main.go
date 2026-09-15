// Command graph-log-watcher watches graph-node container logs and emits
// actionable incident alerts.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/wanliqun/congraph-log-watcher/internal/config"
	"github.com/wanliqun/congraph-log-watcher/internal/runtime"
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
		return runService(args[1:], stdout, stderr)
	case "healthcheck":
		return runHealthcheck(args[1:], stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runService(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "path to the YAML configuration file")
	dryRun := flags.Bool("dry-run", false, "run detection without external notifications")
	if err := flags.Parse(args); err != nil || *path == "" {
		if *path == "" {
			fmt.Fprintln(stderr, "--config is required")
		}
		return 2
	}
	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintf(stderr, "invalid configuration: %v\n", err)
		return 1
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := runtime.Run(ctx, cfg, *dryRun, stdout); err != nil {
		fmt.Fprintf(stderr, "runtime failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "shutdown complete")
	return 0
}

func runHealthcheck(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	url := flags.String("url", "http://127.0.0.1:9108/healthz", "health endpoint URL")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(*url)
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck failed: %v\n", err)
		return 1
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "healthcheck failed: HTTP %d\n", response.StatusCode)
		return 1
	}
	return 0
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
	fmt.Fprintln(w, "usage: graph-log-watcher <check-config|run|healthcheck> [options]")
}
