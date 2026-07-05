package cli

import (
	"bytes"
	"testing"

	"github.com/yasyf/cc-review/internal/version"
)

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
