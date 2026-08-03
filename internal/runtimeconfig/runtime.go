// Package runtimeconfig declares cc-review's exact daemon lifecycle identity.
package runtimeconfig

import (
	ccd "github.com/yasyf/cc-interact/daemon"
	"github.com/yasyf/daemonkit"

	"github.com/yasyf/cc-review/internal/paths"
)

const (
	agentLabel        = "com.yasyf.cc-review"
	teamID            = "SXKCTF23Q2"
	signingIdentifier = "cc-review"
)

// Spec is the one daemonkit identity the launcher and the daemon share. The
// control lane pins the identity cc-review is released under; the serving
// posture is the same-user waiver, because a dev build is unsigned and a
// signed posture would refuse it.
func Spec() (daemonkit.Daemon, error) {
	program, err := daemonkit.Stable()
	if err != nil {
		return daemonkit.Daemon{}, err
	}
	requirement := daemonkit.Requirement{TeamID: teamID, SigningIdentifier: signingIdentifier}
	return ccd.Spec(daemonkit.Daemon{
		Label:   agentLabel,
		Program: program,
		Args:    []string{"daemon"},
		Log:     paths.App().LogPath(),
		Restart: daemonkit.RestartOnFailure,
		Trust: daemonkit.Trust{
			Control: &requirement,
			Serving: daemonkit.ServingSameUser(),
		},
	}), nil
}
