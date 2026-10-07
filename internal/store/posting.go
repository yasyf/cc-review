package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PostingWrite is a comment, or a reply when ReplyID is set, whose GitHub write
// was still posting when the daemon stopped.
type PostingWrite struct {
	ReviewID  string
	CommentID int64
	ReplyID   int64
}

// PostingWrites lists every comment and reply still posting, oldest first, so
// a comment precedes its replies.
func (s *Store) PostingWrites(ctx context.Context) ([]PostingWrite, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT review_id, comment_id, reply_id FROM (
		   SELECT v.review_id, c.id AS comment_id, 0 AS reply_id, c.created_at
		     FROM comments c JOIN review_versions v ON v.id = c.version_id
		    WHERE c.sync_state = ?
		   UNION ALL
		   SELECT v.review_id, c.id, r.id, r.created_at
		     FROM replies r JOIN comments c ON c.id = r.comment_id JOIN review_versions v ON v.id = c.version_id
		    WHERE r.sync_state = ?)
		  ORDER BY created_at, comment_id, reply_id`,
		SyncPosting, SyncPosting)
	if err != nil {
		return nil, fmt.Errorf("list posting writes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []PostingWrite
	for rows.Next() {
		var w PostingWrite
		if err := rows.Scan(&w.ReviewID, &w.CommentID, &w.ReplyID); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// RemoteIDClaimed reports whether a comment or reply in the review already
// mirrors the GitHub comment remoteID.
func (s *Store) RemoteIDClaimed(ctx context.Context, reviewID, remoteID string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM comments c JOIN review_versions v ON v.id = c.version_id
		  WHERE v.review_id = ? AND c.remote_id = ?
		 UNION ALL
		 SELECT 1 FROM replies r JOIN comments c ON c.id = r.comment_id JOIN review_versions v ON v.id = c.version_id
		  WHERE v.review_id = ? AND r.remote_id = ?
		 LIMIT 1`,
		reviewID, remoteID, reviewID, remoteID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
