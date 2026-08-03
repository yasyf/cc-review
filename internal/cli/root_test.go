package cli

import (
	"bytes"
	"testing"

	ccd "github.com/yasyf/cc-interact/daemon"

	"github.com/yasyf/cc-review/internal/testhome"
	"github.com/yasyf/cc-review/internal/version"
)

func TestLauncherCarriesExactDaemonkitIdentity(t *testing.T) {
	testhome.Temp(t) // launcher stages a stable program under <home>/.daemonkit/bin
	l, err := launcher()
	if err != nil {
		t.Fatal(err)
	}
	if l.Daemon.Label != "com.yasyf.cc-review" ||
		len(l.Daemon.Schemas) != 1 || l.Daemon.Schemas[0] != ccd.WireBuild {
		t.Fatalf("launcher identity = %+v", l.Daemon)
	}
	// Paths and RuntimeBuild are required by Launcher.validate but optional to
	// the compiler, so an omission surfaces only on the first daemon call.
	if l.Paths.App == "" || l.RuntimeBuild == "" {
		t.Fatalf("launcher paths = %q, runtime build = %q, want both set", l.Paths.App, l.RuntimeBuild)
	}
	if err := l.Daemon.ValidateForClient(); err != nil {
		t.Fatalf("ValidateForClient: %v", err)
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
			testhome.Temp(t)
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
