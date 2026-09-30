package runtimeconfig

import (
	"testing"

	"github.com/yasyf/cc-review/internal/testhome"
)

func TestControlRestsOnSameUserFloor(t *testing.T) {
	testhome.Temp(t)
	spec, err := Spec()
	if err != nil {
		t.Fatalf("Spec: %v", err)
	}
	if spec.Trust.Control != nil || spec.Trust.Business != nil {
		t.Errorf("Trust = %+v, want nil Control and Business on linux", spec.Trust)
	}
}
