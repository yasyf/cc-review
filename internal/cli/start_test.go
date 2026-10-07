package cli

import (
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/yasyf/cc-review/internal/daemon"
	"github.com/yasyf/cc-review/internal/github"
)

func TestStartExtraLines(t *testing.T) {
	organize := json.RawMessage(`{"id":"7","source":"system","prompt":"Organize this review into chapters and rate per-file risk."}`)
	userReq := json.RawMessage(`{"id":"8","source":"user","prompt":"mark all mechanical changes as viewed"}`)
	for _, tc := range []struct {
		name         string
		channelState string
		offer        bool
		reason       string
		offerErr     error
		stack        *daemon.StackInfo
		pr           *daemon.PRInfo
		githubSetup  string
		organizes    []json.RawMessage
		want         []string
	}{
		{
			name:         "active offer with organize",
			channelState: "active",
			offer:        true,
			reason:       "channel not yet approved",
			want: []string{
				"channel: active",
				`setup: {"offer":true,"reason":"channel not yet approved"}`,
				`organize: ` + string(organize),
			},
			organizes: []json.RawMessage{organize},
		},
		{
			name:         "pending no offer no organize",
			channelState: "pending",
			offer:        false,
			reason:       "already approved",
			want: []string{
				"channel: pending",
				`setup: {"offer":false,"reason":"already approved"}`,
			},
		},
		{
			name:         "inactive offer error degrades to offer false with reason",
			channelState: "inactive",
			offer:        true,
			reason:       "ignored",
			offerErr:     errors.New(`stat "/Library/Application Support": denied`),
			want: []string{
				"channel: inactive",
				`setup: {"offer":false,"reason":"stat \"/Library/Application Support\": denied"}`,
			},
		},
		{
			name:         "multiple requests emit one organize line each",
			channelState: "active",
			offer:        false,
			reason:       "already approved",
			organizes:    []json.RawMessage{organize, userReq},
			want: []string{
				"channel: active",
				`setup: {"offer":false,"reason":"already approved"}`,
				`organize: ` + string(organize),
				`organize: ` + string(userReq),
			},
		},
		{
			name:         "stack review emits a stack line after setup",
			channelState: "active",
			offer:        false,
			reason:       "already approved",
			stack:        &daemon.StackInfo{Trunk: "main", Branches: []string{"feat-a", "feat-b"}},
			want: []string{
				"channel: active",
				`setup: {"offer":false,"reason":"already approved"}`,
				`stack: {"trunk":"main","branches":["feat-a","feat-b"]}`,
			},
		},
		{
			name:         "pr review emits a pr line and the app install url on the setup line",
			channelState: "active",
			offer:        false,
			reason:       "already approved",
			pr:           &daemon.PRInfo{Repo: "o/n", Number: 2, Stack: []int{1, 2, 3}},
			githubSetup:  "https://github.com/apps/cc-review-me/installations/new",
			organizes:    []json.RawMessage{organize},
			want: []string{
				"channel: active",
				`setup: {"github":"https://github.com/apps/cc-review-me/installations/new","offer":false,"reason":"already approved"}`,
				"pr: o/n#2 (stack: #1 #2 #3)",
				`organize: ` + string(organize),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := startExtraLines(daemon.Started{
				ChannelState: tc.channelState, Stack: tc.stack, PR: tc.pr, GitHubSetup: tc.githubSetup, AIRequests: tc.organizes,
			}, tc.offer, tc.reason, tc.offerErr)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("lines = %q, want %q", got, tc.want)
			}
			var setup struct {
				Offer  bool   `json:"offer"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(got[1], "setup: ")), &setup); err != nil {
				t.Fatalf("setup line is not valid JSON: %v", err)
			}
		})
	}
}

func TestResolvePRRef(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "git@github.com:acme/widgets.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil { //nolint:gosec // G204: test helper running git against a test-controlled temp repo.
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	for _, tc := range []struct {
		name string
		raw  string
		dir  string
		want github.PRRef
	}{
		{name: "url ignores the cwd", raw: "https://github.com/o/n/pull/7", dir: t.TempDir(), want: github.PRRef{Repo: github.Repo{Owner: "o", Name: "n"}, Number: 7}},
		{name: "qualified ignores the cwd", raw: "o/n#8", dir: t.TempDir(), want: github.PRRef{Repo: github.Repo{Owner: "o", Name: "n"}, Number: 8}},
		{name: "hash resolves the origin", raw: "#9", dir: repo, want: github.PRRef{Repo: github.Repo{Owner: "acme", Name: "widgets"}, Number: 9}},
		{name: "bare number resolves the origin", raw: "10", dir: repo, want: github.PRRef{Repo: github.Repo{Owner: "acme", Name: "widgets"}, Number: 10}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolvePRRef(t.Context(), tc.raw, tc.dir)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("resolvePRRef(%q) = %+v, want %+v", tc.raw, got, tc.want)
			}
		})
	}
	t.Run("bare form outside a github checkout fails", func(t *testing.T) {
		if _, err := resolvePRRef(t.Context(), "#9", t.TempDir()); err == nil || !strings.Contains(err.Error(), "resolve the repo for --pr #9") {
			t.Fatalf("err = %v, want a repo resolution error", err)
		}
	})
}

func TestStartRejectsPRWithBase(t *testing.T) {
	cmd := newStartCmd()
	cmd.SetArgs([]string{"--pr", "o/n#1", "--base", "main"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "[base pr] were all set") {
		t.Fatalf("err = %v, want cobra's mutually-exclusive refusal", err)
	}
}
