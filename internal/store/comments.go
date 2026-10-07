package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const commentCols = `id, version_id, section_id, branch, pending, file_path, side, start_line, end_line, start_side, end_side, line_content, body, author, status, created_at, updated_at,
	remote_id, remote_thread_id, remote_url, author_login, author_avatar_url, outdated, subject, sync_state, sync_error, edit_seq`

const commentColsC = `c.id, c.version_id, c.section_id, c.branch, c.pending, c.file_path, c.side, c.start_line, c.end_line, c.start_side, c.end_side, c.line_content, c.body, c.author, c.status, c.created_at, c.updated_at,
	c.remote_id, c.remote_thread_id, c.remote_url, c.author_login, c.author_avatar_url, c.outdated, c.subject, c.sync_state, c.sync_error, c.edit_seq`

func scanComment(row interface{ Scan(...any) error }, extra ...any) (Comment, error) {
	var (
		c                 Comment
		pending, outdated int
		created, updated  int64
		remoteID          sql.NullString
	)
	dest := append([]any{
		&c.ID, &c.VersionID, &c.SectionID, &c.Branch, &pending, &c.FilePath, &c.Side, &c.StartLine, &c.EndLine,
		&c.StartSide, &c.EndSide, &c.LineContent, &c.Body, &c.Author, &c.Status, &created, &updated,
		&remoteID, &c.RemoteThreadID, &c.RemoteURL, &c.AuthorLogin, &c.AuthorAvatarURL, &outdated, &c.Subject, &c.SyncState, &c.SyncError, &c.EditSeq,
	},
		extra...)
	if err := row.Scan(dest...); err != nil {
		return Comment{}, err
	}
	c.Pending = pending != 0
	c.Outdated = outdated != 0
	c.RemoteID = remoteID.String
	c.CreatedAt = fromUnix(created)
	c.UpdatedAt = fromUnix(updated)
	return c, nil
}

// ErrStaleSection reports a comment insert against a section whose version the
// review has already superseded.
var ErrStaleSection = errors.New("comment section belongs to a superseded version")

// CreateComment inserts a comment and returns its id. In one transaction it
// first rejects a section no longer on the review's latest version
// (ErrStaleSection) — the single-writer connection serializes that against
// CreateVersion, so a version minted mid-insert can't strand the comment.
func (s *Store) CreateComment(ctx context.Context, c Comment) (int64, error) {
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin comment tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var latestVersionID int64
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM review_versions
		 WHERE review_id = (SELECT review_id FROM review_versions WHERE id = ?)
		 ORDER BY version_number DESC LIMIT 1`, c.VersionID).Scan(&latestVersionID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("resolve latest version: %w", err)
	}
	if c.VersionID != latestVersionID {
		return 0, ErrStaleSection
	}
	id, err := insertComment(ctx, tx, c, now)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit comment: %w", err)
	}
	return id, nil
}

func insertComment(ctx context.Context, tx *sql.Tx, c Comment, now time.Time) (int64, error) {
	res, err := tx.ExecContext(ctx,
		`INSERT INTO comments(review_id, version_id, section_id, branch, pending, file_path, side, start_line, end_line, start_side, end_side, line_content, body, author, status, created_at, updated_at,
		                      remote_id, remote_thread_id, remote_url, author_login, author_avatar_url, outdated, subject, sync_state, sync_error)
		 VALUES((SELECT review_id FROM review_versions WHERE id=?),?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.VersionID, c.VersionID, c.SectionID, c.Branch, boolInt(c.Pending), c.FilePath, c.Side, c.StartLine, c.EndLine, c.StartSide, c.EndSide,
		c.LineContent, c.Body, defaultStr(c.Author, AuthorUser), defaultStr(c.Status, "open"), unix(now), unix(now),
		nullString(c.RemoteID), c.RemoteThreadID, c.RemoteURL, c.AuthorLogin, c.AuthorAvatarURL, boolInt(c.Outdated),
		defaultStr(c.Subject, "line"), defaultStr(c.SyncState, SyncLocal), c.SyncError)
	if err != nil {
		return 0, fmt.Errorf("create comment: %w", err)
	}
	return res.LastInsertId()
}

// UpsertRemoteComment inserts or overwrites the review's comment mirroring
// c.RemoteID. A posting or failed row keeps its body, status, and sync state
// until outbound acknowledges the local edit. created reports an insert.
func (s *Store) UpsertRemoteComment(ctx context.Context, c Comment) (saved Comment, created bool, err error) {
	err = s.ApplyRemote(ctx, func(rt *RemoteTx) error {
		saved, created, err = rt.UpsertComment(ctx, c)
		return err
	})
	return saved, created, err
}

func upsertRemoteComment(ctx context.Context, tx *sql.Tx, c Comment) (Comment, bool, error) {
	if c.RemoteID == "" {
		return Comment{}, false, errors.New("upsert remote comment: empty remote id")
	}
	now := time.Now()
	c.SyncState, c.SyncError = SyncSynced, ""
	var id int64
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM comments WHERE review_id=(SELECT review_id FROM review_versions WHERE id=?) AND remote_id=?`,
		c.VersionID, c.RemoteID).Scan(&id)
	created := errors.Is(err, sql.ErrNoRows)
	switch {
	case created:
		if id, err = insertComment(ctx, tx, c, now); err != nil {
			return Comment{}, false, err
		}
	case err != nil:
		return Comment{}, false, fmt.Errorf("lookup remote comment: %w", err)
	default:
		if _, err := tx.ExecContext(ctx,
			`UPDATE comments SET version_id=?, section_id=?, branch=?, pending=?, file_path=?, side=?, start_line=?, end_line=?,
			        start_side=?, end_side=?, line_content=?, author=?, updated_at=?,
			        remote_thread_id=?, remote_url=?, author_login=?, author_avatar_url=?, outdated=?, subject=?,
			        body=CASE WHEN sync_state IN (?, ?) THEN body ELSE ? END,
			        status=CASE WHEN sync_state IN (?, ?) THEN status ELSE ? END,
			        sync_error=CASE WHEN sync_state IN (?, ?) THEN sync_error ELSE ? END,
			        sync_state=CASE WHEN sync_state IN (?, ?) THEN sync_state ELSE ? END
			  WHERE id=?`,
			c.VersionID, c.SectionID, c.Branch, boolInt(c.Pending), c.FilePath, c.Side, c.StartLine, c.EndLine,
			c.StartSide, c.EndSide, c.LineContent, defaultStr(c.Author, AuthorUser), unix(now),
			c.RemoteThreadID, c.RemoteURL, c.AuthorLogin, c.AuthorAvatarURL, boolInt(c.Outdated), defaultStr(c.Subject, "line"),
			SyncPosting, SyncFailed, c.Body,
			SyncPosting, SyncFailed, defaultStr(c.Status, "open"),
			SyncPosting, SyncFailed, c.SyncError,
			SyncPosting, SyncFailed, c.SyncState, id); err != nil {
			return Comment{}, false, fmt.Errorf("update remote comment: %w", err)
		}
	}
	row, err := scanComment(tx.QueryRowContext(ctx, `SELECT `+commentCols+` FROM comments WHERE id=?`, id))
	if err != nil {
		return Comment{}, false, fmt.Errorf("reread remote comment: %w", err)
	}
	return row, created, nil
}

// CommentByRemoteID returns the review's comment mirroring a GitHub comment,
// or ErrNotFound.
func (s *Store) CommentByRemoteID(ctx context.Context, reviewID, remoteID string) (Comment, error) {
	return commentByRemoteID(ctx, s.db, reviewID, remoteID)
}

func commentByRemoteID(ctx context.Context, q execQueryer, reviewID, remoteID string) (Comment, error) {
	c, err := scanComment(q.QueryRowContext(ctx,
		`SELECT `+commentCols+` FROM comments WHERE review_id=? AND remote_id=?`, reviewID, remoteID))
	if errors.Is(err, sql.ErrNoRows) {
		return Comment{}, ErrNotFound
	}
	return c, err
}

// SetCommentSync records a comment's outbound sync transition. Empty remoteID,
// threadID, and url keep the stored values, so a failure or a retry never
// erases a link an earlier post established.
func (s *Store) SetCommentSync(ctx context.Context, id int64, state, remoteID, threadID, url, syncErr string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE comments SET sync_state=?, sync_error=?, remote_id=COALESCE(?, remote_id),
		        remote_thread_id=COALESCE(NULLIF(?, ''), remote_thread_id), remote_url=COALESCE(NULLIF(?, ''), remote_url), updated_at=?
		  WHERE id=?`,
		state, syncErr, nullString(remoteID), threadID, url, unix(time.Now()), id)
	if err != nil {
		return fmt.Errorf("set comment sync: %w", err)
	}
	return requireRow(res, "comment", id)
}

// AckCommentSync records outbound's synced or failed result for the write that
// pushed the comment as of edit seq. A result for an older seq leaves the row
// posting for the newer write; remote ids and url are recorded either way.
func (s *Store) AckCommentSync(ctx context.Context, id, seq int64, state, remoteID, threadID, url, syncErr string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE comments SET sync_state=CASE WHEN edit_seq=? THEN ? ELSE sync_state END,
		        sync_error=CASE WHEN edit_seq=? THEN ? ELSE sync_error END, remote_id=COALESCE(?, remote_id),
		        remote_thread_id=COALESCE(NULLIF(?, ''), remote_thread_id), remote_url=COALESCE(NULLIF(?, ''), remote_url), updated_at=?
		  WHERE id=?`,
		seq, state, seq, syncErr, nullString(remoteID), threadID, url, unix(time.Now()), id)
	if err != nil {
		return fmt.Errorf("ack comment sync: %w", err)
	}
	return requireRow(res, "comment", id)
}

func requireRow(res sql.Result, table string, id int64) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%s %d: %w", table, id, ErrNotFound)
	}
	return nil
}

// GetComment returns a comment by id, or ErrNotFound.
func (s *Store) GetComment(ctx context.Context, id int64) (Comment, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+commentCols+` FROM comments WHERE id=?`, id)
	c, err := scanComment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Comment{}, ErrNotFound
	}
	return c, err
}

// ListCommentsByVersion returns every comment on a version, oldest first.
func (s *Store) ListCommentsByVersion(ctx context.Context, versionID int64) ([]Comment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+commentCols+` FROM comments WHERE version_id=? ORDER BY created_at ASC, id ASC`, versionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateCommentStatus sets a comment's status (open|resolved). On a GitHub
// thread the same write bumps the edit seq and turns a synced comment posting,
// so a poll never reverts the status before outbound mirrors it.
func (s *Store) UpdateCommentStatus(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE comments SET status=?, updated_at=?, edit_seq=edit_seq+(remote_thread_id<>''),
		        sync_state=CASE WHEN sync_state=? AND remote_thread_id<>'' THEN ? ELSE sync_state END
		  WHERE id=?`, status, unix(time.Now()), SyncSynced, SyncPosting, id)
	if err != nil {
		return fmt.Errorf("update comment status: %w", err)
	}
	return nil
}

// UpdateCommentBody edits a comment's body. The same write bumps the edit seq
// and turns a synced comment posting, so a poll never reverts the edit before
// outbound mirrors it.
func (s *Store) UpdateCommentBody(ctx context.Context, id int64, body string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE comments SET body=?, updated_at=?, edit_seq=edit_seq+1, sync_state=CASE WHEN sync_state=? THEN ? ELSE sync_state END
		  WHERE id=?`, body, unix(time.Now()), SyncSynced, SyncPosting, id)
	if err != nil {
		return fmt.Errorf("update comment body: %w", err)
	}
	return nil
}

// ResolveCommentContext returns the review id and version number a comment
// belongs to, for tagging events. Returns ErrNotFound when the comment is absent.
func (s *Store) ResolveCommentContext(ctx context.Context, commentID int64) (reviewID string, versionNumber int, err error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT v.review_id, v.version_number
		   FROM comments c JOIN review_versions v ON v.id = c.version_id
		  WHERE c.id=?`, commentID)
	err = row.Scan(&reviewID, &versionNumber)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, ErrNotFound
	}
	return reviewID, versionNumber, err
}

// StrandedComment is an open comment on a superseded version, carried into
// feedback with the version it was written against.
type StrandedComment struct {
	Comment       Comment
	VersionNumber int
}

func scanStrandedComment(row interface{ Scan(...any) error }) (StrandedComment, error) {
	var sc StrandedComment
	c, err := scanComment(row, &sc.VersionNumber)
	if err != nil {
		return StrandedComment{}, err
	}
	sc.Comment = c
	return sc, nil
}

// ListStrandedOpenComments returns the review's open comments on versions after
// afterVersion and before beforeVersion — threads a version bump superseded
// before they reached a submit — each tagged with its origin version.
func (s *Store) ListStrandedOpenComments(ctx context.Context, reviewID string, afterVersion, beforeVersion int) ([]StrandedComment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+commentColsC+`, v.version_number
		   FROM comments c JOIN review_versions v ON v.id = c.version_id
		  WHERE v.review_id=? AND c.status='open' AND v.version_number > ? AND v.version_number < ?
		  ORDER BY v.version_number ASC, c.created_at ASC, c.id ASC`,
		reviewID, afterVersion, beforeVersion)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []StrandedComment
	for rows.Next() {
		sc, err := scanStrandedComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
