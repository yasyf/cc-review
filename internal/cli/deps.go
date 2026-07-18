package cli

import (
	"context"

	"github.com/yasyf/cc-interact/cmd"
	ccd "github.com/yasyf/cc-interact/daemon"
	"github.com/yasyf/cc-interact/procs"

	"github.com/yasyf/cc-review/internal/daemon"
	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/version"
)

// launcher lazily starts and version-gates the review daemon, re-execing this
// binary's hidden `daemon` subcommand detached.
func launcher() ccd.Launcher {
	return ccd.Launcher{Paths: paths.App(), Version: version.String(), Args: []string{"daemon"}}
}

func newControlClient() *ccd.Client { return ccd.NewClient(paths.App().SocketPath()) }

// ensureCurrent lazily starts or upgrades the daemon, blocking until a current
// one answers. The user-facing review commands gate on it before each RPC.
func ensureCurrent(context.Context) error { return launcher().EnsureCurrent(ccd.UpgradeTimeout) }

// deps wires the cc-interact substrate commands to cc-review's host: its daemon
// launcher, control client, window identity, terminal event, and channel tools.
func deps() cmd.Deps {
	return cmd.Deps{
		Paths:                  paths.App(),
		Version:                version.String(),
		NewClient:              newControlClient,
		EnsureCurrent:          func(context.Context) error { return launcher().EnsureCurrent(ccd.UpgradeTimeout) },
		EnsureCurrentIfRunning: func() error { return launcher().EnsureCurrentIfRunning() },
		ClaudePID:              procs.ClaudePID,
		WindowAlive:            procs.LiveClaude,
		TerminalEvent:          func(t string) bool { return t == "submit" },
		Serve:                  func(ctx context.Context) error { return daemon.Serve(ctx, 0) },
		ChannelTools:           channelTools,
	}
}
