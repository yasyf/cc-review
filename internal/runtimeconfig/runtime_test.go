package runtimeconfig

import (
	"testing"

	ccd "github.com/yasyf/cc-interact/daemon"
	"github.com/yasyf/daemonkit"

	"github.com/yasyf/cc-review/internal/testhome"
)

func TestSpecIsExact(t *testing.T) {
	testhome.Temp(t) // Stable() resolves a program under <home>/.daemonkit/bin
	spec, err := Spec()
	if err != nil {
		t.Fatalf("Spec: %v", err)
	}
	if spec.Label != agentLabel {
		t.Errorf("Label = %q, want %q", spec.Label, agentLabel)
	}
	if spec.Restart != daemonkit.RestartOnFailure {
		t.Errorf("Restart = %v, want RestartOnFailure", spec.Restart)
	}
	if len(spec.Schemas) != 1 || spec.Schemas[0] != ccd.WireBuild {
		t.Errorf("Schemas = %v, want exactly [%q]", spec.Schemas, ccd.WireBuild)
	}
	if c := spec.Trust.Control; c == nil || c.TeamID != teamID || c.SigningIdentifier != signingIdentifier {
		t.Errorf("Trust.Control = %+v, want team %q identifier %q", c, teamID, signingIdentifier)
	}
	// Open runs ValidateForClient, and an unstated Trust.Serving is what it refuses.
	if err := spec.ValidateForClient(); err != nil {
		t.Errorf("ValidateForClient: %v", err)
	}
}
