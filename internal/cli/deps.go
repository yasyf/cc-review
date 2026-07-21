package cli

import (
	"context"

	"github.com/yasyf/cc-interact/cmd"
	ccd "github.com/yasyf/cc-interact/daemon"
	"github.com/yasyf/cc-interact/procs"

	"github.com/yasyf/cc-review/internal/daemon"
	approle "github.com/yasyf/cc-review/internal/daemonrole"
	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/version"
)

// launcher lazily starts and version-gates the review daemon, re-execing this
// binary's hidden `daemon` subcommand detached.
func launcher() ccd.Launcher {
	return ccd.Launcher{
		Paths: paths.App(), Version: version.Build(), LifecycleBuild: version.Build(),
		Args: []string{"daemon"}, DaemonRole: approle.Classifier(),
	}
}

func newControlClient(ctx context.Context) (*ccd.Client, error) { return launcher().NewClient(ctx) }

// ensureCurrent lazily starts or upgrades the daemon, blocking until a current
// one answers. The user-facing review commands gate on it before each RPC.
func ensureCurrent(ctx context.Context) error {
	return launcher().EnsureCurrent(ctx, ccd.UpgradeTimeout)
}

// deps wires the cc-interact substrate commands to cc-review's host: its daemon
// launcher, control client, window identity, terminal event, and channel tools.
func deps() cmd.Deps {
	return cmd.Deps{
		Paths:                  paths.App(),
		Version:                version.Build(),
		NewClient:              newControlClient,
		EnsureCurrent:          func(ctx context.Context) error { return launcher().EnsureCurrent(ctx, ccd.UpgradeTimeout) },
		EnsureCurrentIfRunning: func(ctx context.Context) error { return launcher().EnsureCurrentIfRunning(ctx) },
		ClaudePID:              procs.ClaudePID,
		WindowAlive:            procs.LiveClaude,
		TerminalEvent:          func(t string) bool { return t == "submit" },
		Serve:                  func(ctx context.Context) error { return daemon.Serve(ctx, 0) },
		ChannelTools:           channelTools,
	}
}
