package runtimeconfig

import "github.com/yasyf/daemonkit"

const (
	teamID            = "SXKCTF23Q2"
	signingIdentifier = "cc-review"
)

func control() *daemonkit.Requirement {
	return &daemonkit.Requirement{TeamID: teamID, SigningIdentifier: signingIdentifier}
}
