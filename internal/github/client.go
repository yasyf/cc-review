// Package github calls GitHub's REST and GraphQL APIs for pull-request reviews.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/yasyf/cc-review/internal/version"
)

const (
	restURL        = "https://api.github.com"
	graphQLURL     = "https://api.github.com/graphql"
	requestTimeout = 30 * time.Second
)

var (
	// ErrNotFound reports a 404, or a GraphQL error typed NOT_FOUND.
	ErrNotFound = errors.New("not found")
	// ErrUnauthorized reports a 401: the token does not authenticate here.
	ErrUnauthorized = errors.New("unauthorized")
)

// TokenSource yields the bearer token a request authenticates with.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

type userTokenSource struct {
	mu    sync.Mutex
	token string
}

// UserTokenSource resolves the user's token from `gh auth token` on first use
// and caches it for the life of the process.
func UserTokenSource() TokenSource { return &userTokenSource{} }

func (s *userTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" {
		return s.token, nil
	}
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err != nil {
		return "", fmt.Errorf("gh auth token: %w", err)
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", errors.New("gh auth token printed nothing")
	}
	s.token = token
	return token, nil
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL points the client at alternate REST and GraphQL endpoints.
func WithBaseURL(rest, graphql string) Option {
	return func(c *Client) {
		c.rest = strings.TrimSuffix(rest, "/")
		c.graphql = graphql
	}
}

// Client issues authenticated GitHub API requests with one token source.
type Client struct {
	rest    string
	graphql string
	http    *http.Client
	tokens  TokenSource
}

// New builds a client against api.github.com authenticating through ts.
func New(ts TokenSource, opts ...Option) *Client {
	c := &Client{rest: restURL, graphql: graphQLURL, http: &http.Client{}, tokens: ts}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// StatusError is a request that reached GitHub and came back non-2xx. It
// unwraps to ErrNotFound on 404 and ErrUnauthorized on 401.
type StatusError struct {
	Method  string
	URL     string
	Status  int
	Message string
}

func (e *StatusError) Error() string {
	message := e.Message
	if message == "" {
		message = http.StatusText(e.Status)
	}
	return fmt.Sprintf("github: %s %s: %d %s", e.Method, e.URL, e.Status, message)
}

func (e *StatusError) Unwrap() error {
	switch e.Status {
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusUnauthorized:
		return ErrUnauthorized
	}
	return nil
}

// GraphQLMessage is one entry of a GraphQL response's errors array.
type GraphQLMessage struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// GraphQLError is a 200 response whose body carried GraphQL errors. It
// unwraps to ErrNotFound when any entry is typed NOT_FOUND.
type GraphQLError struct {
	Messages []GraphQLMessage
}

func (e *GraphQLError) Error() string {
	texts := make([]string, 0, len(e.Messages))
	for _, m := range e.Messages {
		texts = append(texts, m.Message)
	}
	return "github: graphql: " + strings.Join(texts, "; ")
}

func (e *GraphQLError) Unwrap() error {
	for _, m := range e.Messages {
		if m.Type == "NOT_FOUND" {
			return ErrNotFound
		}
	}
	return nil
}

// REST sends body as JSON to path under the REST root and decodes the
// response into out. A nil body sends none; a nil out discards the response.
func (c *Client) REST(ctx context.Context, method, path string, body, out any) error {
	target := c.rest + "/" + strings.TrimPrefix(path, "/")
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("github: encode %s %s: %w", method, target, err)
		}
	}
	resp, err := c.send(ctx, method, target, payload)
	if err != nil || out == nil {
		return err
	}
	if err := json.Unmarshal(resp, out); err != nil {
		return fmt.Errorf("github: decode %s %s: %w", method, target, err)
	}
	return nil
}

// GraphQL posts query with vars and decodes the response's data into out. A
// body carrying GraphQL errors returns *GraphQLError even though GitHub
// answered 200.
func (c *Client) GraphQL(ctx context.Context, query string, vars map[string]any, out any) error {
	payload, err := json.Marshal(struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables,omitempty"`
	}{query, vars})
	if err != nil {
		return fmt.Errorf("github: encode graphql request: %w", err)
	}
	resp, err := c.send(ctx, http.MethodPost, c.graphql, payload)
	if err != nil {
		return err
	}
	var envelope struct {
		Data   json.RawMessage  `json:"data"`
		Errors []GraphQLMessage `json:"errors"`
	}
	if err := json.Unmarshal(resp, &envelope); err != nil {
		return fmt.Errorf("github: decode graphql response: %w", err)
	}
	if len(envelope.Errors) > 0 {
		return &GraphQLError{Messages: envelope.Errors}
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("github: decode graphql data: %w", err)
	}
	return nil
}

func (c *Client) send(ctx context.Context, method, target string, body []byte) ([]byte, error) {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("github: resolve token: %w", err)
	}
	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, target, reader)
	if err != nil {
		return nil, fmt.Errorf("github: build request %s %s: %w", method, target, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "cc-review/"+version.Tag())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: %s %s: %w", method, target, err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("github: read %s %s: %w", method, target, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var envelope struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(payload, &envelope)
		return nil, &StatusError{Method: method, URL: target, Status: resp.StatusCode, Message: envelope.Message}
	}
	return payload, nil
}
