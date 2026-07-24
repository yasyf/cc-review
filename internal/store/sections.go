package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const sectionCols = `id, version_id, position, branch, parent_branch, base_ref, head_ref, pending, patch_path, files_json`

func scanSection(row interface{ Scan(...any) error }) (Section, error) {
	var (
		s       Section
		pending int
	)
	if err := row.Scan(&s.ID, &s.VersionID, &s.Position, &s.Branch, &s.ParentBranch,
		&s.BaseRef, &s.HeadRef, &pending, &s.PatchPath, &s.FilesJSON); err != nil {
		return Section{}, err
	}
	s.Pending = pending != 0
	return s, nil
}

// ListSections returns a version's sections in position order (trunk-most
// first, pending last).
func (s *Store) ListSections(ctx context.Context, versionID int64) ([]Section, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+sectionCols+` FROM version_sections WHERE version_id=? ORDER BY position ASC`, versionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Section
	for rows.Next() {
		sec, err := scanSection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sec)
	}
	return out, rows.Err()
}

// GetSection returns a section by id, or ErrNotFound.
func (s *Store) GetSection(ctx context.Context, id int64) (Section, error) {
	sec, err := scanSection(s.db.QueryRowContext(ctx, `SELECT `+sectionCols+` FROM version_sections WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Section{}, ErrNotFound
	}
	return sec, err
}

// UpdateSectionPatchPath sets a section's on-disk patch path after the snapshot
// has been written (the path embeds the version number and section position).
func (s *Store) UpdateSectionPatchPath(ctx context.Context, id int64, patchPath string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE version_sections SET patch_path=? WHERE id=?`, patchPath, id); err != nil {
		return fmt.Errorf("update section patch path: %w", err)
	}
	return nil
}
