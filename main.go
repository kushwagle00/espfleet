package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/kushwagle00/espfleet/internal/cli"
	"github.com/kushwagle00/espfleet/internal/transport"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == transport.WorkerArgument && os.Getenv("ESPFLEET_WORKER") == "1" {
		os.Exit(transport.Worker(os.Stdin, os.Stdout))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr, cli.Dependencies{})
	stop()
	os.Exit(code)
}
