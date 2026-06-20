// Package generated detects auto-generated and vendored files in a working-tree
// snapshot. The flags are advisory: the web UI uses them to auto-collapse noise
// (lockfiles, *.pb.go, vendor/, …) so the real diff stands out.
package generated

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	enry "github.com/go-enry/go-enry/v2"
	"github.com/yasyf/cc-interact/vcs"
)

const maxContentBytes = 1 << 20

// Flags marks whether a file is auto-generated or vendored.
type Flags struct {
	Generated bool
	Vendored  bool
}

// attrState is the tri-state of one git linguist attribute: unspecified leaves
// enry's verdict in place, set forces the flag on, unset forces it off.
type attrState int

const (
	attrUnspecified attrState = iota
	attrSet
	attrUnset
)

// fileAttrs is one file's linguist-generated/linguist-vendored attribute states.
type fileAttrs struct {
	generated attrState
	vendored  attrState
}

// Classify computes per-file generated/vendored flags for a snapshot's files,
// keyed by path. Layer 1 is enry's filename+content heuristics; layer 2 overlays
// explicit .gitattributes linguist-generated/linguist-vendored marks via
// `git check-attr`, which beat enry (a repo can force a flag on or off).
//
// The check-attr overlay is best-effort: if git is absent or the command errors,
// the enry results stand. This advisory signal must never fail review capture,
// so this is the one place the package tolerates a silent degradation.
func Classify(ctx context.Context, repoRoot string, files []vcs.FileChange) map[string]Flags {
	out := make(map[string]Flags, len(files))
	paths := make([]string, 0, len(files))
	for _, f := range files {
		if f.Status == "D" {
			continue
		}
		content := readContent(repoRoot, f.Path)
		out[f.Path] = Flags{
			Generated: enry.IsGenerated(f.Path, content),
			Vendored:  enry.IsVendor(f.Path),
		}
		paths = append(paths, f.Path)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".git")); err != nil {
		return out
	}
	overlay, err := checkAttr(ctx, repoRoot, paths)
	if err != nil {
		return out
	}
	for path, attrs := range overlay {
		f := out[path]
		switch attrs.generated {
		case attrSet:
			f.Generated = true
		case attrUnset:
			f.Generated = false
		}
		switch attrs.vendored {
		case attrSet:
			f.Vendored = true
		case attrUnset:
			f.Vendored = false
		}
		out[path] = f
	}
	return out
}

// readContent reads the post-image of path under repoRoot, capped at
// maxContentBytes. An unreadable file or a binary one (NUL byte present) yields
// nil content, leaving enry to fall back to its filename-only checks.
func readContent(repoRoot, path string) []byte {
	data, err := os.ReadFile(filepath.Join(repoRoot, path)) //nolint:gosec // G304: repoRoot and path come from the user's own reviewed working tree; reading their content is the whole point of generated-file classification.
	if err != nil {
		return nil
	}
	if len(data) > maxContentBytes {
		data = data[:maxContentBytes]
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil
	}
	return data
}

// checkAttr runs `git check-attr` for the linguist marks over the given paths,
// feeding them NUL-separated on stdin and parsing the -z output.
func checkAttr(ctx context.Context, repoRoot string, paths []string) (map[string]fileAttrs, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "check-attr", "-z", "--stdin", //nolint:gosec // G204: fixed git subcommand; only repoRoot (the user's own repo) varies, and paths are passed via stdin, not argv.
		"linguist-generated", "linguist-vendored")
	var stdin bytes.Buffer
	for _, p := range paths {
		stdin.WriteString(p)
		stdin.WriteByte(0)
	}
	cmd.Stdin = &stdin
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git check-attr: %w", err)
	}
	return parseCheckAttr(out), nil
}

// parseCheckAttr decodes `git check-attr -z` output: a flat stream of
// NUL-separated path\0attr\0value\0 triples (the stream is NUL-terminated, so a
// trailing empty field falls outside the last full triple and is ignored). git
// reports each attribute's value as set/true, unset/false, or unspecified.
func parseCheckAttr(out []byte) map[string]fileAttrs {
	fields := bytes.Split(out, []byte{0})
	attrs := make(map[string]fileAttrs)
	for i := 0; i+2 < len(fields); i += 3 {
		path := string(fields[i])
		state := attrStateFromValue(string(fields[i+2]))
		fa := attrs[path]
		switch string(fields[i+1]) {
		case "linguist-generated":
			fa.generated = state
		case "linguist-vendored":
			fa.vendored = state
		}
		attrs[path] = fa
	}
	return attrs
}

func attrStateFromValue(value string) attrState {
	switch value {
	case "set", "true":
		return attrSet
	case "unset", "false":
		return attrUnset
	default:
		return attrUnspecified
	}
}
