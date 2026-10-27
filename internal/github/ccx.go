package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var rfc3339 = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`)

// RateLimitError is a read ccx or gh refused for a rate limit, ccx's quota
// floor included. Reset is when the quota refills, zero when the refusal did
// not name it.
type RateLimitError struct {
	Message string
	Reset   time.Time
}

// Error quotes the refusal's first line.
func (e *RateLimitError) Error() string { return "github: " + e.Message }

// Unwrap reports ErrRateLimited.
func (e *RateLimitError) Unwrap() error { return ErrRateLimited }

func readThroughCCX(ctx context.Context, repo Repo, payload []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ccx", "vcs", "gh", "--", "api", "graphql", "--input", "-")
	cmd.Env = append(os.Environ(), "GH_REPO="+repo.String(), "CCX_CALLER=cc-review")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil {
		return out, nil
	}
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	for _, line := range lines {
		if !strings.Contains(strings.ToLower(line), "rate limit") {
			continue
		}
		limit := &RateLimitError{Message: line}
		if reset, perr := time.Parse(time.RFC3339, rfc3339.FindString(line)); perr == nil {
			limit.Reset = reset
		}
		return nil, limit
	}
	var envelope struct {
		Errors []GraphQLMessage `json:"errors"`
	}
	if json.Unmarshal(out, &envelope) == nil && len(envelope.Errors) > 0 {
		return nil, &GraphQLError{Messages: envelope.Errors}
	}
	return nil, fmt.Errorf("github: ccx vcs gh graphql on %s: %w: %s", repo, err, lines[0])
}
