package vcs

import (
	"fmt"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

func parseFiles(patch string) ([]FileChange, error) {
	parsed, _, err := gitdiff.Parse(strings.NewReader(patch))
	if err != nil {
		return nil, fmt.Errorf("parse patch: %w", err)
	}
	var out []FileChange
	for _, f := range parsed {
		switch {
		case f.IsNew:
			out = append(out, FileChange{Path: f.NewName, Status: "A"})
		case f.IsDelete:
			out = append(out, FileChange{Path: f.OldName, Status: "D"})
		case f.IsRename:
			out = append(out, FileChange{Path: f.NewName, OldPath: f.OldName, Status: "R"})
		case f.IsCopy:
			out = append(out, FileChange{Path: f.NewName, OldPath: f.OldName, Status: "C"})
		default:
			out = append(out, FileChange{Path: f.NewName, Status: "M"})
		}
	}
	return out, nil
}
