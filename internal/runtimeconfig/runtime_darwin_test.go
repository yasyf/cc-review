package runtimeconfig

import (
	"testing"

	"github.com/yasyf/cc-review/internal/testhome"
)

func TestControlPinsReleaseIdentity(t *testing.T) {
	testhome.Temp(t)
	spec, err := Spec()
	if err != nil {
		t.Fatalf("Spec: %v", err)
	}
	if c := spec.Trust.Control; c == nil || c.TeamID != teamID || c.SigningIdentifier != signingIdentifier {
		t.Errorf("Trust.Control = %+v, want team %q identifier %q", c, teamID, signingIdentifier)
	}
}
