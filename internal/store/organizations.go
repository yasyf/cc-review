package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var risks = map[string]bool{"high": true, "medium": true, "low": true, "mechanical": true}

var lineLevels = map[string]bool{"focus": true, "mechanical": true}

// Validate enforces exact coverage against a version's changed paths: every
// path in exactly one chapter, no unknown paths, and a known risk per file.
// The error enumerates every offending path so Claude can self-correct.
func (o Organization) Validate(versionPaths []string) error {
	return o.validate(versionPaths, false)
}

// ValidatePartial validates one streaming, in-progress submit: every submitted
// file must be known, unique, and validly rated, but files not yet placed in a
// chapter are allowed. The agent's final non-partial Validate enforces full
// coverage, so the complete-organization invariant holds at the terminal state.
func (o Organization) ValidatePartial(versionPaths []string) error {
	return o.validate(versionPaths, true)
}

func (o Organization) validate(versionPaths []string, partial bool) error {
	known := make(map[string]bool, len(versionPaths))
	for _, p := range versionPaths {
		known[p] = true
	}
	seen := make(map[string]bool)
	var unknown, duplicated []string
	for _, ch := range o.Chapters {
		for _, f := range ch.Files {
			if !risks[f.Risk] {
				return fmt.Errorf("organization: file %s has unknown risk %q (want high | medium | low | mechanical)", f.Path, f.Risk)
			}
			for _, ln := range f.Lines {
				if !lineLevels[ln.Level] {
					return fmt.Errorf("organization: file %s line note has unknown level %q (want focus | mechanical)", f.Path, ln.Level)
				}
				if ln.Start < 1 || ln.Start > ln.End {
					return fmt.Errorf("organization: file %s has invalid line range %d-%d (want 1 <= start <= end)", f.Path, ln.Start, ln.End)
				}
			}
			switch {
			case !known[f.Path]:
				unknown = append(unknown, f.Path)
			case seen[f.Path]:
				duplicated = append(duplicated, f.Path)
			}
			seen[f.Path] = true
		}
	}
	var missing []string
	if !partial {
		for _, p := range versionPaths {
			if !seen[p] {
				missing = append(missing, p)
			}
		}
	}
	if len(missing) == 0 && len(unknown) == 0 && len(duplicated) == 0 {
		return nil
	}
	var problems []string
	if len(missing) > 0 {
		problems = append(problems, "missing paths: "+strings.Join(missing, ", "))
	}
	if len(unknown) > 0 {
		problems = append(problems, "unknown paths: "+strings.Join(unknown, ", "))
	}
	if len(duplicated) > 0 {
		problems = append(problems, "paths in more than one chapter: "+strings.Join(duplicated, ", "))
	}
	return fmt.Errorf("organization: every changed file must appear in exactly one chapter — %s", strings.Join(problems, "; "))
}

// UpsertOrganization stores (or replaces) a section's organization.
func (s *Store) UpsertOrganization(ctx context.Context, sectionID int64, org Organization) error {
	b, err := json.Marshal(org)
	if err != nil {
		return fmt.Errorf("encode organization: %w", err)
	}
	now := unix(time.Now())
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO organizations(section_id, chapters_json, created_at, updated_at) VALUES(?,?,?,?)
		 ON CONFLICT(section_id) DO UPDATE SET chapters_json=excluded.chapters_json, updated_at=excluded.updated_at`,
		sectionID, string(b), now, now); err != nil {
		return fmt.Errorf("upsert organization: %w", err)
	}
	return nil
}

// LatestOrganizationForKey returns the newest organization for a section key
// across the review's versions and the section that owns it; ok is false when
// no version has organized that section. The stack's positions are stable, so a
// key resolves the same section across versions.
func (s *Store) LatestOrganizationForKey(ctx context.Context, reviewID, sectionKey string) (Organization, Section, bool, error) {
	var (
		chaptersJSON string
		sec          Section
		pending      int
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT o.chapters_json, vs.id, vs.version_id, vs.position, vs.branch, vs.parent_branch, vs.base_ref, vs.head_ref, vs.pending, vs.patch_path, vs.files_json
		 FROM organizations o
		 JOIN version_sections vs ON vs.id = o.section_id
		 JOIN review_versions v ON v.id = vs.version_id
		 WHERE v.review_id=? AND (CASE WHEN vs.pending=1 THEN '' ELSE vs.branch END)=?
		 ORDER BY v.version_number DESC LIMIT 1`, reviewID, sectionKey).
		Scan(&chaptersJSON, &sec.ID, &sec.VersionID, &sec.Position, &sec.Branch, &sec.ParentBranch,
			&sec.BaseRef, &sec.HeadRef, &pending, &sec.PatchPath, &sec.FilesJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return Organization{}, Section{}, false, nil
	}
	if err != nil {
		return Organization{}, Section{}, false, err
	}
	sec.Pending = pending != 0
	var org Organization
	if err := json.Unmarshal([]byte(chaptersJSON), &org); err != nil {
		return Organization{}, Section{}, false, fmt.Errorf("section %d: decode organization: %w", sec.ID, err)
	}
	return org, sec, true, nil
}

// GetOrganization returns a section's organization; ok is false when none has
// been submitted yet.
func (s *Store) GetOrganization(ctx context.Context, sectionID int64) (Organization, bool, error) {
	var chaptersJSON string
	err := s.db.QueryRowContext(ctx,
		`SELECT chapters_json FROM organizations WHERE section_id=?`, sectionID).Scan(&chaptersJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return Organization{}, false, nil
	}
	if err != nil {
		return Organization{}, false, err
	}
	var org Organization
	if err := json.Unmarshal([]byte(chaptersJSON), &org); err != nil {
		return Organization{}, false, fmt.Errorf("section %d: decode organization: %w", sectionID, err)
	}
	return org, true, nil
}

// GetOrganizationsByVersion returns every organized section of a version, keyed
// by section id.
func (s *Store) GetOrganizationsByVersion(ctx context.Context, versionID int64) (map[int64]Organization, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT o.section_id, o.chapters_json FROM organizations o
		 JOIN version_sections vs ON vs.id = o.section_id WHERE vs.version_id=?`, versionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int64]Organization)
	for rows.Next() {
		var (
			sectionID    int64
			chaptersJSON string
		)
		if err := rows.Scan(&sectionID, &chaptersJSON); err != nil {
			return nil, err
		}
		var org Organization
		if err := json.Unmarshal([]byte(chaptersJSON), &org); err != nil {
			return nil, fmt.Errorf("section %d: decode organization: %w", sectionID, err)
		}
		out[sectionID] = org
	}
	return out, rows.Err()
}
