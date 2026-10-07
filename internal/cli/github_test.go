package cli

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/yasyf/cc-review/internal/ghapp"
	"github.com/yasyf/cc-review/internal/github"
	"github.com/yasyf/cc-review/internal/testhome"
)

func TestPrintAppStatus(t *testing.T) {
	app := ghapp.App{ID: 7, Slug: "cc-review-octo", BotLogin: "cc-review-octo[bot]", Owner: "octo"}
	repo := github.Repo{Owner: "acme", Name: "widgets"}
	boom := errors.New("boom")
	cases := []struct {
		name     string
		tokenErr error
		want     string
		wantErr  error
	}{
		{"installed", nil, "app: cc-review-octo\nbot: cc-review-octo[bot]\ninstall: installed on acme/widgets\n", nil},
		{
			"not installed",
			fmt.Errorf("%w on acme/widgets", ghapp.ErrNotInstalled),
			"app: cc-review-octo\nbot: cc-review-octo[bot]\ninstall: not installed on acme/widgets; install at https://github.com/apps/cc-review-octo/installations/new\n",
			nil,
		},
		{"mint failure", boom, "", boom},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			err := printAppStatus(&out, app, repo, c.tokenErr)
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if out.String() != c.want {
				t.Errorf("output = %q, want %q", out.String(), c.want)
			}
		})
	}
}

func TestGitHubStatusWithoutApp(t *testing.T) {
	testhome.Temp(t)
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"github", "status"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "app: not set up; run `cc-review github setup`\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}
