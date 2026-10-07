package ghapptest

import (
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/yasyf/cc-review/internal/ghapp"
	"github.com/yasyf/cc-review/internal/testhome"
)

func TestInstall(t *testing.T) {
	testhome.Temp(t)
	want := ghapp.App{ID: 7, Slug: "cc-review-octo", BotLogin: "cc-review-octo[bot]", Owner: "octo"}
	key := Install(t, want)
	got, ok, err := ghapp.Load()
	if err != nil || !ok || got != want {
		t.Fatalf("Load() = %+v, %v, %v; want %+v", got, ok, err, want)
	}
	if stored, err := keyring.Get("cc-review.github-app", "7"); err != nil || stored != PEM(key) {
		t.Errorf("keychain pem = %q, %v; want the installed key", stored, err)
	}
}
