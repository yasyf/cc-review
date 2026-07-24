package store

import (
	"context"
	"fmt"
	"time"
)

// AttributionRange maps an inclusive span of new-side lines in one file of a
// version's diff to the turn that wrote them; a zero TurnID means unattributed.
type AttributionRange struct {
	Start  int   `json:"start"`
	End    int   `json:"end"`
	TurnID int64 `json:"turnId,omitempty"`
}

// PutAttributions stores (or replaces) a section's per-file attribution
// ranges, one row per file.
func (s *Store) PutAttributions(ctx context.Context, sectionID int64, byFile map[string][]AttributionRange) error {
	now := time.Now().UnixMilli()
	for path, ranges := range byFile {
		encoded, err := encodeAttributionRanges(ranges)
		if err != nil {
			return fmt.Errorf("encode attribution ranges for %s: %w", path, err)
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT OR REPLACE INTO turn_attributions(section_id, file_path, ranges_json, created_at) VALUES(?,?,?,?)`,
			sectionID, path, encoded, now); err != nil {
			return fmt.Errorf("put attributions for %s: %w", path, err)
		}
	}
	return nil
}

// SessionAttribution is one file's attribution ranges within one review
// version, joined back to the owning review for the activity export.
type SessionAttribution struct {
	ReviewID string
	Version  int
	FilePath string
	Ranges   []AttributionRange
}

// ListAttributionsBySession returns every attribution row of a session's
// reviews, joined through review versions and ordered by (review, version,
// file path) — the (review_id, version, file_path) dimension of the activity
// export.
func (s *Store) ListAttributionsBySession(ctx context.Context, sessionID string) ([]SessionAttribution, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, v.version_number, a.file_path, a.ranges_json
		 FROM turn_attributions a
		 JOIN version_sections vs ON vs.id = a.section_id
		 JOIN review_versions v ON v.id = vs.version_id
		 JOIN subjects r ON r.id = v.review_id
		 WHERE r.session_id = ?
		 ORDER BY r.id, v.version_number, a.file_path`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list session attributions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SessionAttribution
	for rows.Next() {
		var (
			sa         SessionAttribution
			rangesJSON string
		)
		if err := rows.Scan(&sa.ReviewID, &sa.Version, &sa.FilePath, &rangesJSON); err != nil {
			return nil, err
		}
		var err error
		if sa.Ranges, err = decodeAttributionRanges(rangesJSON); err != nil {
			return nil, fmt.Errorf("decode attribution ranges for %s v%d %s: %w", sa.ReviewID, sa.Version, sa.FilePath, err)
		}
		out = append(out, sa)
	}
	return out, rows.Err()
}

// ListAttributionsBySection returns a section's attribution ranges keyed by
// file path.
func (s *Store) ListAttributionsBySection(ctx context.Context, sectionID int64) (map[string][]AttributionRange, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT file_path, ranges_json FROM turn_attributions WHERE section_id=?`, sectionID)
	if err != nil {
		return nil, fmt.Errorf("list attributions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	byFile := make(map[string][]AttributionRange)
	for rows.Next() {
		var path, rangesJSON string
		if err := rows.Scan(&path, &rangesJSON); err != nil {
			return nil, err
		}
		ranges, err := decodeAttributionRanges(rangesJSON)
		if err != nil {
			return nil, fmt.Errorf("section %d: decode attribution ranges for %s: %w", sectionID, path, err)
		}
		byFile[path] = ranges
	}
	return byFile, rows.Err()
}
