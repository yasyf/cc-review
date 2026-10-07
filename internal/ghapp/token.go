package ghapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/yasyf/cc-review/internal/github"
)

const (
	jwtBackdate  = 60 * time.Second
	jwtLifetime  = 9 * time.Minute
	tokenRefresh = 5 * time.Minute
)

var (
	appClientOptions []github.Option
	now              = time.Now
)

type installKey struct {
	app  int64
	repo github.Repo
}

type installationToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

var cache = struct {
	sync.Mutex
	installations map[installKey]int64
	tokens        map[int64]installationToken
}{installations: map[installKey]int64{}, tokens: map[int64]installationToken{}}

type jwtSource struct{ app App }

func (s jwtSource) Token(context.Context) (string, error) { return s.app.signJWT(now()) }

type tokenSource struct {
	app  App
	repo github.Repo
}

// TokenSource mints installation tokens for repo, caching each installation's
// token until five minutes before it expires. A repo the app is not installed on
// fails with ErrNotInstalled.
func (a App) TokenSource(repo github.Repo) github.TokenSource {
	return tokenSource{app: a, repo: repo}
}

func (ts tokenSource) Token(ctx context.Context) (string, error) {
	cache.Lock()
	defer cache.Unlock()
	key := installKey{app: ts.app.ID, repo: ts.repo}
	id, known := cache.installations[key]
	if tok, ok := cache.tokens[id]; known && ok && now().Before(tok.ExpiresAt.Add(-tokenRefresh)) {
		return tok.Token, nil
	}
	c := github.New(jwtSource{app: ts.app}, appClientOptions...)
	if !known {
		var inst struct {
			ID int64 `json:"id"`
		}
		if err := c.REST(ctx, http.MethodGet, "/repos/"+ts.repo.String()+"/installation", nil, &inst); err != nil {
			return "", ts.mintError(err)
		}
		id = inst.ID
		cache.installations[key] = id
	}
	var tok installationToken
	if err := c.REST(ctx, http.MethodPost, "/app/installations/"+strconv.FormatInt(id, 10)+"/access_tokens", nil, &tok); err != nil {
		delete(cache.installations, key)
		delete(cache.tokens, id)
		return "", ts.mintError(err)
	}
	cache.tokens[id] = tok
	return tok.Token, nil
}

func (ts tokenSource) mintError(err error) error {
	if errors.Is(err, github.ErrNotFound) {
		return fmt.Errorf("%w on %s: install it at %s", ErrNotInstalled, ts.repo, ts.app.InstallURL())
	}
	return fmt.Errorf("mint %s installation token for %s: %w", ts.app.Slug, ts.repo, err)
}

func (a App) signJWT(at time.Time) (string, error) {
	pem, err := a.privateKey()
	if err != nil {
		return "", err
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(pem))
	if err != nil {
		return "", fmt.Errorf("parse github app %d key: %w", a.ID, err)
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		Issuer:    strconv.FormatInt(a.ID, 10),
		IssuedAt:  jwt.NewNumericDate(at.Add(-jwtBackdate)),
		ExpiresAt: jwt.NewNumericDate(at.Add(jwtLifetime)),
	}).SignedString(key)
	if err != nil {
		return "", fmt.Errorf("sign github app %d jwt: %w", a.ID, err)
	}
	return signed, nil
}
