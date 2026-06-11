package store

import (
	"context"
	"encoding/json"
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

// PutAttributions stores (or replaces) a version's per-file attribution
// ranges, one row per file.
func (s *Store) PutAttributions(ctx context.Context, versionID int64, byFile map[string][]AttributionRange) error {
	now := time.Now().UnixMilli()
	for path, ranges := range byFile {
		b, err := json.Marshal(ranges)
		if err != nil {
			return fmt.Errorf("encode attribution ranges for %s: %w", path, err)
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT OR REPLACE INTO turn_attributions(version_id, file_path, ranges_json, created_at) VALUES(?,?,?,?)`,
			versionID, path, string(b), now); err != nil {
			return fmt.Errorf("put attributions for %s: %w", path, err)
		}
	}
	return nil
}

// ListAttributionsByVersion returns a version's attribution ranges keyed by
// file path.
func (s *Store) ListAttributionsByVersion(ctx context.Context, versionID int64) (map[string][]AttributionRange, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT file_path, ranges_json FROM turn_attributions WHERE version_id=?`, versionID)
	if err != nil {
		return nil, fmt.Errorf("list attributions: %w", err)
	}
	defer rows.Close()
	byFile := make(map[string][]AttributionRange)
	for rows.Next() {
		var path, rangesJSON string
		if err := rows.Scan(&path, &rangesJSON); err != nil {
			return nil, err
		}
		var ranges []AttributionRange
		if err := json.Unmarshal([]byte(rangesJSON), &ranges); err != nil {
			return nil, fmt.Errorf("version %d: decode attribution ranges for %s: %w", versionID, path, err)
		}
		byFile[path] = ranges
	}
	return byFile, rows.Err()
}
