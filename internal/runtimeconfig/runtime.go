// Package runtimeconfig declares cc-review's exact daemon lifecycle identity.
package runtimeconfig

import (
	ccd "github.com/yasyf/cc-interact/daemon"
	"github.com/yasyf/daemonkit"

	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/version"
)

const agentLabel = "com.yasyf.cc-review"

// Spec is the one daemonkit identity the launcher and the daemon share.
func Spec() (daemonkit.Daemon, error) {
	program, err := daemonkit.Stable(version.Build())
	if err != nil {
		return daemonkit.Daemon{}, err
	}
	return ccd.Spec(daemonkit.Daemon{
		Label:   agentLabel,
		Program: program,
		Args:    []string{"daemon"},
		Log:     paths.App().LogPath(),
		Restart: daemonkit.RestartOnFailure,
	}), nil
}
