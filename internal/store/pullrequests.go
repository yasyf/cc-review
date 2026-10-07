package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
)

const pullRequestCols = `review_id, number, node_id, title, body, state, url, author_login, head_ref_name, head_sha, base_ref_name,
	draft, mergeable, checks_json, reviewers_json, viewer_is_author, updated_at`

func scanPullRequest(row interface{ Scan(...any) error }) (PullRequest, error) {
	var (
		pr                        PullRequest
		draft, viewerIsAuthor     int
		checksJSON, reviewersJSON string
		updated                   int64
	)
	if err := row.Scan(&pr.ReviewID, &pr.Number, &pr.NodeID, &pr.Title, &pr.Body, &pr.State, &pr.URL, &pr.AuthorLogin,
		&pr.HeadRefName, &pr.HeadSHA, &pr.BaseRefName, &draft, &pr.Mergeable, &checksJSON, &reviewersJSON,
		&viewerIsAuthor, &updated); err != nil {
		return PullRequest{}, err
	}
	checks, err := decodePRChecks(checksJSON)
	if err != nil {
		return PullRequest{}, fmt.Errorf("pull request #%d: decode checks: %w", pr.Number, err)
	}
	reviewers, err := decodePRReviewers(reviewersJSON)
	if err != nil {
		return PullRequest{}, fmt.Errorf("pull request #%d: decode reviewers: %w", pr.Number, err)
	}
	pr.Draft = draft != 0
	pr.ViewerIsAuthor = viewerIsAuthor != 0
	pr.Checks, pr.Reviewers = checks, reviewers
	pr.UpdatedAt = fromUnix(updated)
	return pr, nil
}

// UpsertPullRequest stores a PR's latest GitHub metadata under its review.
// changed is false when the stored row matches apart from UpdatedAt, which
// GitHub bumps on every comment, so only a real metadata change emits
// pr.updated.
func (s *Store) UpsertPullRequest(ctx context.Context, pr PullRequest) (changed bool, err error) {
	err = s.ApplyRemote(ctx, func(rt *RemoteTx) error {
		changed, err = rt.UpsertPullRequest(ctx, pr)
		return err
	})
	return changed, err
}

func upsertPullRequest(ctx context.Context, tx *sql.Tx, pr PullRequest) (bool, error) {
	if pr.Checks == nil {
		pr.Checks = []PRCheck{}
	}
	if pr.Reviewers == nil {
		pr.Reviewers = []PRReviewer{}
	}
	pr.UpdatedAt = fromUnix(unix(pr.UpdatedAt))
	checksJSON, err := encodePRChecks(pr.Checks)
	if err != nil {
		return false, fmt.Errorf("upsert pull request #%d: %w", pr.Number, err)
	}
	reviewersJSON, err := encodePRReviewers(pr.Reviewers)
	if err != nil {
		return false, fmt.Errorf("upsert pull request #%d: %w", pr.Number, err)
	}
	stored, err := scanPullRequest(tx.QueryRowContext(ctx,
		`SELECT `+pullRequestCols+` FROM pull_requests WHERE review_id=? AND number=?`, pr.ReviewID, pr.Number))
	found := err == nil
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return false, err
	case reflect.DeepEqual(stored, pr):
		return false, nil
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO pull_requests(`+pullRequestCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(review_id, number) DO UPDATE SET node_id=excluded.node_id, title=excluded.title, body=excluded.body,
		   state=excluded.state, url=excluded.url, author_login=excluded.author_login, head_ref_name=excluded.head_ref_name,
		   head_sha=excluded.head_sha, base_ref_name=excluded.base_ref_name, draft=excluded.draft, mergeable=excluded.mergeable,
		   checks_json=excluded.checks_json, reviewers_json=excluded.reviewers_json,
		   viewer_is_author=excluded.viewer_is_author, updated_at=excluded.updated_at`,
		pr.ReviewID, pr.Number, pr.NodeID, pr.Title, pr.Body, pr.State, pr.URL, pr.AuthorLogin, pr.HeadRefName, pr.HeadSHA,
		pr.BaseRefName, boolInt(pr.Draft), pr.Mergeable, checksJSON, reviewersJSON, boolInt(pr.ViewerIsAuthor),
		unix(pr.UpdatedAt)); err != nil {
		return false, fmt.Errorf("upsert pull request #%d: %w", pr.Number, err)
	}
	stored.UpdatedAt = pr.UpdatedAt
	return !found || !reflect.DeepEqual(stored, pr), nil
}

// PullRequests returns every PR cached under a review, by number.
func (s *Store) PullRequests(ctx context.Context, reviewID string) ([]PullRequest, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+pullRequestCols+` FROM pull_requests WHERE review_id=? ORDER BY number ASC`, reviewID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PullRequest
	for rows.Next() {
		pr, err := scanPullRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}
