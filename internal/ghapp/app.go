// Package ghapp creates the per-user cc-review GitHub App via the manifest flow
// and mints the installation tokens Claude's writes post with.
package ghapp

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"

	"github.com/zalando/go-keyring"

	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/persistedjson"
)

const (
	keyringService = "cc-review.github-app"

	appSchemaIdentity    = "dev.yasyf.cc-review.github-app"
	appSchemaDescriptor  = "payload:{id:int64,slug:string,bot_login:string,owner:string}"
	appSchemaFingerprint = "dev.yasyf.cc-review.github-app.ac082d0aab9e75d398dc69f171f0e771b9fb07a622f406ad6dd45647c2babf38"
)

// ErrNotInstalled reports that the app has no installation on a repository;
// the wrapping error names the install URL.
var ErrNotInstalled = errors.New("cc-review GitHub App is not installed")

// App is the user's cc-review GitHub App as created by the manifest flow.
type App struct {
	ID       int64
	Slug     string
	BotLogin string
	Owner    string
}

type appV1 struct {
	ID       *int64  `json:"id"`
	Slug     *string `json:"slug"`
	BotLogin *string `json:"bot_login"`
	Owner    *string `json:"owner"`
}

// Load reads the app identity, reporting false when setup has not run.
func Load() (App, bool, error) {
	data, err := os.ReadFile(paths.GitHubApp())
	if errors.Is(err, fs.ErrNotExist) {
		return App{}, false, nil
	}
	if err != nil {
		return App{}, false, fmt.Errorf("read github app: %w", err)
	}
	v, err := persistedjson.Decode[appV1](data, appSchemaIdentity, appSchemaFingerprint)
	if err != nil {
		return App{}, false, fmt.Errorf("decode %s: %w", paths.GitHubApp(), err)
	}
	if v.ID == nil || v.Slug == nil || v.BotLogin == nil || v.Owner == nil {
		return App{}, false, fmt.Errorf("decode %s: id, slug, bot_login and owner are required", paths.GitHubApp())
	}
	return App{ID: *v.ID, Slug: *v.Slug, BotLogin: *v.BotLogin, Owner: *v.Owner}, true, nil
}

// InstallURL is where a user installs the app on an account or repository.
func (a App) InstallURL() string {
	return "https://github.com/apps/" + a.Slug + "/installations/new"
}

// Save stores the app's private key in the Keychain and its identity in
// github-app.json, where Load reads it.
func Save(a App, pem string) error {
	if err := keyring.Set(keyringService, strconv.FormatInt(a.ID, 10), pem); err != nil {
		return fmt.Errorf("store github app key in keychain: %w", err)
	}
	data, err := persistedjson.Encode(appSchemaIdentity, appSchemaFingerprint, appV1{
		ID: &a.ID, Slug: &a.Slug, BotLogin: &a.BotLogin, Owner: &a.Owner,
	})
	if err != nil {
		return fmt.Errorf("encode github app: %w", err)
	}
	if err := paths.EnsureStateDir(); err != nil {
		return err
	}
	return persistedjson.WriteFile(paths.GitHubApp(), data)
}

func (a App) privateKey() (string, error) {
	pem, err := keyring.Get(keyringService, strconv.FormatInt(a.ID, 10))
	if err != nil {
		return "", fmt.Errorf("read github app %d key from keychain: %w", a.ID, err)
	}
	return pem, nil
}
