// Package runtimeconfig declares cc-review's exact daemon lifecycle identity.
package runtimeconfig

import (
	ccd "github.com/yasyf/cc-interact/daemon"
	"github.com/yasyf/daemonkit"

	"github.com/yasyf/cc-review/internal/paths"
)

const agentLabel = "com.yasyf.cc-review"

// Spec is the one daemonkit identity the launcher and the daemon share. On
// darwin the control lane pins the identity cc-review is released under; linux
// has no code identity to pin, so there every lane rests on the same-user
// floor. The serving posture is the same-user waiver everywhere, because a dev
// build is unsigned and a signed posture would refuse it.
func Spec() (daemonkit.Daemon, error) {
	program, err := daemonkit.Stable()
	if err != nil {
		return daemonkit.Daemon{}, err
	}
	return ccd.Spec(daemonkit.Daemon{
		Label:   agentLabel,
		Program: program,
		Args:    []string{"daemon"},
		Log:     paths.App().LogPath(),
		Restart: daemonkit.RestartOnFailure,
		Trust: daemonkit.Trust{
			Control: control(),
			Serving: daemonkit.ServingSameUser(),
		},
	}), nil
}
