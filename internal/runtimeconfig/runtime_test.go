package runtimeconfig

import (
	"testing"

	"github.com/yasyf/daemonkit/trust"

	"github.com/yasyf/cc-review/internal/testhome"
)

func TestRuntimeIdentityIsExact(t *testing.T) {
	testhome.Temp(t) // Agent stages a stable program under <home>/.daemonkit/bin
	roles := Roles()
	if roles.Business != trust.UnprotectedRole || roles.Lifecycle != lifecycleRole || roles.StopControl != stopControlRole {
		t.Fatalf("roles = %+v", roles)
	}
	policy, err := TrustPolicy()
	if err != nil {
		t.Fatalf("TrustPolicy: %v", err)
	}
	if !policy.AllowsUnprotected() || !policy.AllowsReceipt(roles.Lifecycle) ||
		!policy.AllowsReadiness(roles.Lifecycle) || !policy.AllowsStop(roles.StopControl) {
		t.Fatal("trust policy lacks an exact declared authority")
	}
	agent, err := Agent()
	if err != nil {
		t.Fatalf("Agent: %v", err)
	}
	if agent.Label != agentLabel || agent.RestartPolicy == 0 {
		t.Fatalf("agent = %+v", agent)
	}
	if _, err := agent.Plist(); err != nil {
		t.Fatalf("Agent.Plist: %v", err)
	}
}
