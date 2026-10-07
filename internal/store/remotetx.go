package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// RemoteTx writes GitHub-sourced rows and the events announcing them in one
// transaction, so a row never commits without its event queued.
type RemoteTx struct{ tx *sql.Tx }

// ApplyRemote runs fn in one RemoteTx and commits when fn returns nil.
func (s *Store) ApplyRemote(ctx context.Context, fn func(*RemoteTx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin remote tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(&RemoteTx{tx: tx}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit remote tx: %w", err)
	}
	return nil
}

// UpsertComment is UpsertRemoteComment inside the transaction.
func (t *RemoteTx) UpsertComment(ctx context.Context, c Comment) (Comment, bool, error) {
	return upsertRemoteComment(ctx, t.tx, c)
}

// UpsertReply is UpsertRemoteReply inside the transaction.
func (t *RemoteTx) UpsertReply(ctx context.Context, r Reply) (Reply, bool, error) {
	return upsertRemoteReply(ctx, t.tx, r)
}

// UpsertPullRequest is Store.UpsertPullRequest inside the transaction.
func (t *RemoteTx) UpsertPullRequest(ctx context.Context, pr PullRequest) (bool, error) {
	return upsertPullRequest(ctx, t.tx, pr)
}

// CommentByRemoteID is Store.CommentByRemoteID inside the transaction.
func (t *RemoteTx) CommentByRemoteID(ctx context.Context, reviewID, remoteID string) (Comment, error) {
	return commentByRemoteID(ctx, t.tx, reviewID, remoteID)
}

// ReplyByRemoteID returns the review's reply mirroring a GitHub comment, or
// ErrNotFound.
func (t *RemoteTx) ReplyByRemoteID(ctx context.Context, reviewID, remoteID string) (Reply, error) {
	return replyByRemoteID(ctx, t.tx, reviewID, remoteID)
}

// ListRepliesByComment is Store.ListRepliesByComment inside the transaction.
func (t *RemoteTx) ListRepliesByComment(ctx context.Context, commentID int64) ([]Reply, error) {
	return listReplies(ctx, t.tx, commentID)
}

// AdoptComment gives c's remote ids to the review's matching posting or
// failed comment that outbound never linked to GitHub, and marks it synced.
func (t *RemoteTx) AdoptComment(ctx context.Context, reviewID string, c Comment) (Comment, bool, error) {
	var id int64
	err := t.tx.QueryRowContext(ctx,
		`SELECT id FROM comments
		  WHERE review_id=? AND remote_id IS NULL AND sync_state IN (?, ?) AND author=? AND branch=? AND file_path=?
		    AND COALESCE(NULLIF(end_side, ''), side)=? AND subject=? AND (subject='file' OR end_line=?) AND body=?
		  ORDER BY id LIMIT 1`,
		reviewID, SyncPosting, SyncFailed, c.Author, c.Branch, c.FilePath, c.EndSide, c.Subject, c.EndLine, c.Body).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Comment{}, false, nil
	}
	if err != nil {
		return Comment{}, false, fmt.Errorf("adopt comment: %w", err)
	}
	if _, err := t.tx.ExecContext(ctx,
		`UPDATE comments SET remote_id=?, remote_thread_id=?, remote_url=?, sync_state=?, sync_error='' WHERE id=?`,
		c.RemoteID, c.RemoteThreadID, c.RemoteURL, SyncSynced, id); err != nil {
		return Comment{}, false, fmt.Errorf("adopt comment: %w", err)
	}
	adopted, err := scanComment(t.tx.QueryRowContext(ctx, `SELECT `+commentCols+` FROM comments WHERE id=?`, id))
	return adopted, err == nil, err
}

// AdoptReply gives r's remote ids to the unlinked reply on r.CommentID, or on
// a conversation comment of branch, whose GitHubBody is r.Body, and marks it
// synced.
func (t *RemoteTx) AdoptReply(ctx context.Context, reviewID, branch string, conversation bool, r Reply) (Reply, bool, error) {
	rows, err := t.tx.QueryContext(ctx,
		`SELECT `+replyCols+` FROM replies WHERE id IN (
		   SELECT r.id FROM replies r JOIN comments c ON c.id = r.comment_id
		    WHERE c.review_id=? AND r.remote_id IS NULL AND r.sync_state IN (?, ?) AND r.origin=?
		      AND CASE WHEN ? THEN c.branch=? AND c.subject='file' AND c.file_path='' ELSE r.comment_id=? END)
		  ORDER BY id`,
		reviewID, SyncPosting, SyncFailed, r.Origin, conversation, branch, r.CommentID)
	if err != nil {
		return Reply{}, false, fmt.Errorf("adopt reply: %w", err)
	}
	var match *Reply
	for rows.Next() {
		cand, err := scanReply(rows)
		if err != nil {
			_ = rows.Close()
			return Reply{}, false, fmt.Errorf("adopt reply: %w", err)
		}
		if cand.GitHubBody() == r.Body {
			match = &cand
			break
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return Reply{}, false, fmt.Errorf("adopt reply: %w", err)
	}
	if match == nil {
		return Reply{}, false, nil
	}
	if _, err := t.tx.ExecContext(ctx,
		`UPDATE replies SET remote_id=?, remote_url=?, sync_state=?, sync_error='' WHERE id=?`,
		r.RemoteID, r.RemoteURL, SyncSynced, match.ID); err != nil {
		return Reply{}, false, fmt.Errorf("adopt reply: %w", err)
	}
	adopted, err := scanReply(t.tx.QueryRowContext(ctx, `SELECT `+replyCols+` FROM replies WHERE id=?`, match.ID))
	return adopted, err == nil, err
}

// QueueEvent records an event to append once the transaction commits.
func (t *RemoteTx) QueueEvent(ctx context.Context, reviewID, origin, typ string, payload []byte) error {
	if _, err := t.tx.ExecContext(ctx,
		`INSERT INTO pending_events(review_id, origin, type, payload) VALUES(?,?,?,?)`,
		reviewID, origin, typ, string(payload)); err != nil {
		return fmt.Errorf("queue %s event: %w", typ, err)
	}
	return nil
}

// ImportedPRs returns the numbers of the review's PRs whose first snapshot a
// queued pr.imported event already recorded.
func (t *RemoteTx) ImportedPRs(ctx context.Context, reviewID string) (map[int]bool, error) {
	rows, err := t.tx.QueryContext(ctx,
		`SELECT CAST(json_extract(pr.value, '$.number') AS INTEGER)
		   FROM pending_events e, json_each(e.payload, '$.pullRequests') pr
		  WHERE e.review_id=? AND e.type=?`, reviewID, EventPRImported)
	if err != nil {
		return nil, fmt.Errorf("imported pull requests: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int]bool{}
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("imported pull requests: %w", err)
		}
		out[n] = true
	}
	return out, rows.Err()
}

// PendingEvent is an event a committed RemoteTx queued that has not yet been
// appended to the review's event log.
type PendingEvent struct {
	ID       int64
	ReviewID string
	Origin   string
	Type     string
	Payload  []byte
}

// DedupKey makes appending a pending event a second time a no-op, for when
// the append landed but recording its seq did not.
func (p PendingEvent) DedupKey() string {
	return "pending-event:" + strconv.FormatInt(p.ID, 10)
}

// PendingEvents returns the review's queued events not yet appended, oldest
// first.
func (s *Store) PendingEvents(ctx context.Context, reviewID string) ([]PendingEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, review_id, origin, type, payload FROM pending_events WHERE review_id=? AND seq=0 ORDER BY id ASC`, reviewID)
	if err != nil {
		return nil, fmt.Errorf("pending events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []PendingEvent
	for rows.Next() {
		var (
			p       PendingEvent
			payload string
		)
		if err := rows.Scan(&p.ID, &p.ReviewID, &p.Origin, &p.Type, &payload); err != nil {
			return nil, err
		}
		p.Payload = []byte(payload)
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkEventAppended records the event-log seq a pending event landed at.
func (s *Store) MarkEventAppended(ctx context.Context, id, seq int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE pending_events SET seq=? WHERE id=?`, seq, id)
	if err != nil {
		return fmt.Errorf("mark event appended: %w", err)
	}
	return requireRow(res, "pending event", id)
}
