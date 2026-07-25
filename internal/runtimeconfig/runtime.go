// Package runtimeconfig declares cc-review's exact daemon lifecycle identity.
package runtimeconfig

import (
	"os"

	ccd "github.com/yasyf/cc-interact/daemon"
	"github.com/yasyf/daemonkit/service"
	"github.com/yasyf/daemonkit/trust"

	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/version"
)

const (
	agentLabel        = "com.yasyf.cc-review"
	lifecycleRole     = "com.yasyf.cc-review.lifecycle.v1"
	stopControlRole   = "com.yasyf.cc-review.stop.v1"
	teamID            = "SXKCTF23Q2"
	signingIdentifier = "cc-review"
)

// Roles returns cc-review's exact business and lifecycle authorities.
func Roles() ccd.Roles {
	return ccd.Roles{
		Business: trust.UnprotectedRole, Lifecycle: lifecycleRole, StopControl: stopControlRole,
	}
}

// TrustPolicy returns the immutable signed-peer policy for cc-review.
func TrustPolicy() (trust.TrustPolicy, error) {
	roles := Roles()
	requirement := trust.Requirement{TeamID: teamID, SigningIdentifier: signingIdentifier}
	return trust.NewTrustPolicy(trust.TrustPolicyConfig{
		ExpectedUID: os.Geteuid(), AllowUnprotected: true,
		Roles:          map[trust.PeerRole]trust.Requirement{roles.Lifecycle: requirement, roles.StopControl: requirement},
		StopRoles:      []trust.PeerRole{roles.StopControl},
		ReceiptRoles:   []trust.PeerRole{roles.Lifecycle},
		ReadinessRoles: []trust.PeerRole{roles.Lifecycle},
	})
}

// Agent returns the exact launchd service specification for this executable.
func Agent() (service.Agent, error) {
	executable, err := service.StableProgram("cc-review", version.Build())
	if err != nil {
		return service.Agent{}, err
	}
	return service.Agent{
		Label: agentLabel, Program: executable, Args: []string{"daemon"},
		LogPath: paths.App().LogPath(), RestartPolicy: service.RestartOnFailure,
	}, nil
}
