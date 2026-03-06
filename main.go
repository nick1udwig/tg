package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if err := runE(ctx, args, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func runE(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	root := newRootCommand(stdout, stderr)
	root.Version = version
	root.SetContext(ctx)
	root.SetArgs(args)
	return root.ExecuteContext(ctx)
}
