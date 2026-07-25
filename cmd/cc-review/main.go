// Command cc-review is the local code-review daemon + CLI. The same binary is
// the user-facing CLI, the hidden background daemon, and the hidden plugin hook
// handlers.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/yasyf/cc-review/internal/cli"
	"github.com/yasyf/daemonkit/trust"
)

func main() {
	if recognized, err := trust.RunVerifierChild(os.Args[1:], os.Stdout); recognized {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := cli.NewRootCmd().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
