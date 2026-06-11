package attrib

import (
	"context"
	"fmt"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/vcs"
)

// Link is one tree transition in a version's snapshot chain; TurnID 0 marks an
// untagged gap (manual edits between turns).
type Link struct {
	From   string
	To     string
	TurnID int64
}

type run struct {
	count  int
	turnID int64
}

// Compute replays the chain's tree transitions to build per-file line
// provenance, then maps every added line of versionPatch to the turn that
// wrote it. Provenance is a run-list with an implicit untagged tail, so files
// first seen mid-chain need no known length.
func Compute(ctx context.Context, d vcs.TreeDiffer, chain []Link, versionPatch string) (map[string][]store.AttributionRange, error) {
	prov := make(map[string][]run)
	for _, l := range chain {
		if l.From == l.To {
			continue
		}
		patch, err := d.Diff(ctx, l.From, l.To)
		if err != nil {
			return nil, fmt.Errorf("diff %s..%s: %w", l.From, l.To, err)
		}
		files, _, err := gitdiff.Parse(strings.NewReader(patch))
		if err != nil {
			return nil, fmt.Errorf("parse %s..%s patch: %w", l.From, l.To, err)
		}
		for _, f := range files {
			applyFile(prov, f, l.TurnID)
		}
	}

	files, _, err := gitdiff.Parse(strings.NewReader(versionPatch))
	if err != nil {
		return nil, fmt.Errorf("parse version patch: %w", err)
	}
	byFile := make(map[string][]store.AttributionRange)
	for _, f := range files {
		if f.IsBinary || f.IsDelete {
			continue
		}
		if ranges := addedRanges(f, prov[f.NewName]); len(ranges) > 0 {
			byFile[f.NewName] = ranges
		}
	}
	return byFile, nil
}

func applyFile(prov map[string][]run, f *gitdiff.File, turnID int64) {
	switch {
	case f.IsBinary:
	case f.IsDelete:
		delete(prov, f.OldName)
	case f.IsNew:
		prov[f.NewName] = applyFragments(nil, f.TextFragments, turnID)
	case f.IsRename:
		runs := prov[f.OldName]
		delete(prov, f.OldName)
		prov[f.NewName] = applyFragments(runs, f.TextFragments, turnID)
	default:
		prov[f.NewName] = applyFragments(prov[f.NewName], f.TextFragments, turnID)
	}
}

func applyFragments(old []run, frags []*gitdiff.TextFragment, turnID int64) []run {
	c := &cursor{runs: old}
	var out []run
	oldLine := int64(1)
	for _, frag := range frags {
		gap := frag.OldPosition - oldLine
		if frag.OldLines == 0 {
			gap++
		}
		out = appendRuns(out, c.take(int(gap)))
		oldLine += gap
		for _, ln := range frag.Lines {
			switch ln.Op {
			case gitdiff.OpContext:
				out = appendRuns(out, c.take(1))
				oldLine++
			case gitdiff.OpDelete:
				c.take(1)
				oldLine++
			case gitdiff.OpAdd:
				out = appendRun(out, run{count: 1, turnID: turnID})
			}
		}
	}
	return appendRuns(out, c.rest())
}

type cursor struct {
	runs []run
	idx  int
	off  int
}

func (c *cursor) take(n int) []run {
	var out []run
	for n > 0 && c.idx < len(c.runs) {
		r := c.runs[c.idx]
		taken := min(n, r.count-c.off)
		out = appendRun(out, run{count: taken, turnID: r.turnID})
		c.off += taken
		n -= taken
		if c.off == r.count {
			c.idx++
			c.off = 0
		}
	}
	if n > 0 {
		out = appendRun(out, run{count: n, turnID: 0})
	}
	return out
}

func (c *cursor) rest() []run {
	if c.idx >= len(c.runs) {
		return nil
	}
	r := c.runs[c.idx]
	out := []run{{count: r.count - c.off, turnID: r.turnID}}
	return append(out, c.runs[c.idx+1:]...)
}

func appendRun(runs []run, r run) []run {
	if r.count == 0 {
		return runs
	}
	if n := len(runs); n > 0 && runs[n-1].turnID == r.turnID {
		runs[n-1].count += r.count
		return runs
	}
	return append(runs, r)
}

func appendRuns(runs []run, more []run) []run {
	for _, r := range more {
		runs = appendRun(runs, r)
	}
	return runs
}

func turnAt(runs []run, line int) int64 {
	pos := 0
	for _, r := range runs {
		pos += r.count
		if line <= pos {
			return r.turnID
		}
	}
	return 0
}

func addedRanges(f *gitdiff.File, runs []run) []store.AttributionRange {
	var out []store.AttributionRange
	for _, frag := range f.TextFragments {
		newLine := int(frag.NewPosition)
		for _, ln := range frag.Lines {
			switch ln.Op {
			case gitdiff.OpContext:
				newLine++
			case gitdiff.OpAdd:
				tid := turnAt(runs, newLine)
				if n := len(out); n > 0 && out[n-1].End == newLine-1 && out[n-1].TurnID == tid {
					out[n-1].End = newLine
				} else {
					out = append(out, store.AttributionRange{Start: newLine, End: newLine, TurnID: tid})
				}
				newLine++
			}
		}
	}
	return out
}
