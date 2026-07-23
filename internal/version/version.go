// Package version exposes build metadata, injected at link time via -ldflags.
package version

import (
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"

	dkversion "github.com/yasyf/daemonkit/version"
)

var releaseTriple = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

var (
	// Version is the semantic version, set by -ldflags at release time.
	Version = "dev"
	// Commit is the short git SHA, set by -ldflags at release time.
	Commit = ""
)

// Newer reports whether a is a strictly newer build than b. A leading v?X.Y.Z
// triple is compared numerically; any suffix is ignored, so "v0.8.0-1-gHASH"
// ties with "v0.8.0", and ties are never newer. A string with no release
// triple is a dev build and ranks newest: dev beats every release and ties
// with dev. That polarity is deliberate — a dev daemon is never evicted, and a
// dev binary always takes over a release daemon — preserving the dev-daemon
// workflow.
func Newer(a, b string) bool {
	ta, aRelease := parseTriple(a)
	tb, bRelease := parseTriple(b)
	if !aRelease {
		return bRelease
	}
	if !bRelease {
		return false
	}
	for i := range ta {
		if ta[i] != tb[i] {
			return ta[i] > tb[i]
		}
	}
	return false
}

func parseTriple(s string) ([3]int, bool) {
	m := releaseTriple.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return [3]int{}, false
	}
	var t [3]int
	for i := range t {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return [3]int{}, false
		}
		t[i] = n
	}
	return t, true
}

// Tag renders the stamped version alone — for a release build, exactly the
// git tag ("v0.19.2"), no commit suffix. `--version` prints this and
// plugin/scripts/install-binary.sh pins its freshness check to it.
func Tag() string {
	if Version == "dev" {
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			return bi.Main.Version
		}
	}
	return Version
}

// Build returns the exact runtime release identity for the running binary.
func Build() string { return dkversion.Running(Tag()) }

// String renders a human-readable version line: the tag plus the commit when
// one was stamped.
func String() string {
	v := Tag()
	if Commit != "" {
		v += " (" + Commit + ")"
	}
	return v
}
