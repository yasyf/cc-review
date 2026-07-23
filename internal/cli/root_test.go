package cli

import (
	"bytes"
	"slices"
	"testing"

	ccd "github.com/yasyf/cc-interact/daemon"

	"github.com/yasyf/cc-review/internal/version"
)

func TestRootCarriesExactStopControlRole(t *testing.T) {
	root := NewRootCmd()
	control, _, err := root.Find([]string{ccd.StopControlCommand})
	if err != nil {
		t.Fatal(err)
	}
	if control == root || !control.Hidden || control.Use != ccd.StopControlCommand {
		t.Fatalf("stop control command = use %q hidden %v", control.Use, control.Hidden)
	}
	l := launcher()
	if l.WireBuild != ccd.WireBuild || l.RuntimeBuild != version.Build() ||
		!slices.Equal(l.StopArgs, []string{ccd.StopControlCommand}) {
		t.Fatalf("launcher identity = wire %q runtime %q stop args %v", l.WireBuild, l.RuntimeBuild, l.StopArgs)
	}
}

// TestVersionFlag pins the installer contract: `cc-review --version` prints
// exactly the stamped tag on one line, with no commit suffix —
// plugin/scripts/install-binary.sh matches it against v<plugin.json version>.
func TestVersionFlag(t *testing.T) {
	cases := []struct {
		name    string
		version string
		commit  string
		want    string
	}{
		{"release build prints the bare tag", "v0.19.2", "abc1234", "v0.19.2\n"},
		{"dev build prints dev", "dev", "abc1234", "dev\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			defer func(v, c string) { version.Version, version.Commit = v, c }(version.Version, version.Commit)
			version.Version, version.Commit = tc.version, tc.commit
			root := NewRootCmd()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetArgs([]string{"--version"})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if got := out.String(); got != tc.want {
				t.Errorf("--version output = %q, want %q", got, tc.want)
			}
		})
	}
}
