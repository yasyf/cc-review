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

// Validate enforces exact coverage against a version's changed paths: every
// path in exactly one chapter, no unknown paths, and a known risk per file.
// The error enumerates every offending path so Claude can self-correct.
func (o Organization) Validate(versionPaths []string) error {
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
	for _, p := range versionPaths {
		if !seen[p] {
			missing = append(missing, p)
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

// UpsertOrganization stores (or replaces) a version's organization.
func (s *Store) UpsertOrganization(ctx context.Context, versionID int64, org Organization) error {
	b, err := json.Marshal(org)
	if err != nil {
		return fmt.Errorf("encode organization: %w", err)
	}
	now := unix(time.Now())
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO organizations(version_id, chapters_json, created_at, updated_at) VALUES(?,?,?,?)
		 ON CONFLICT(version_id) DO UPDATE SET chapters_json=excluded.chapters_json, updated_at=excluded.updated_at`,
		versionID, string(b), now, now); err != nil {
		return fmt.Errorf("upsert organization: %w", err)
	}
	return nil
}

// LatestOrganization returns the review's newest organization and the version
// that owns it (the highest version_number with an organization row); ok is
// false when no version of the review has been organized.
func (s *Store) LatestOrganization(ctx context.Context, reviewID string) (Organization, Version, bool, error) {
	var (
		chaptersJSON string
		v            Version
		created      int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT o.chapters_json, v.id, v.review_id, v.version_number, v.branch, v.base_ref, v.patch_path, v.files_json, v.created_at
		 FROM organizations o JOIN review_versions v ON v.id = o.version_id
		 WHERE v.review_id=? ORDER BY v.version_number DESC LIMIT 1`, reviewID).
		Scan(&chaptersJSON, &v.ID, &v.ReviewID, &v.VersionNumber, &v.Branch, &v.BaseRef, &v.PatchPath, &v.FilesJSON, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Organization{}, Version{}, false, nil
	}
	if err != nil {
		return Organization{}, Version{}, false, err
	}
	v.CreatedAt = fromUnix(created)
	var org Organization
	if err := json.Unmarshal([]byte(chaptersJSON), &org); err != nil {
		return Organization{}, Version{}, false, fmt.Errorf("version %d: decode organization: %w", v.ID, err)
	}
	return org, v, true, nil
}

// GetOrganization returns a version's organization; ok is false when none has
// been submitted yet.
func (s *Store) GetOrganization(ctx context.Context, versionID int64) (Organization, bool, error) {
	var chaptersJSON string
	err := s.db.QueryRowContext(ctx,
		`SELECT chapters_json FROM organizations WHERE version_id=?`, versionID).Scan(&chaptersJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return Organization{}, false, nil
	}
	if err != nil {
		return Organization{}, false, err
	}
	var org Organization
	if err := json.Unmarshal([]byte(chaptersJSON), &org); err != nil {
		return Organization{}, false, fmt.Errorf("version %d: decode organization: %w", versionID, err)
	}
	return org, true, nil
}
