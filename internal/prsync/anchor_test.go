package prsync

import (
	"testing"

	ccevent "github.com/yasyf/cc-interact/event"

	"github.com/yasyf/cc-review/internal/ghapp"
	"github.com/yasyf/cc-review/internal/github"
	"github.com/yasyf/cc-review/internal/store"
)

func TestThreadAnchor(t *testing.T) {
	type want struct {
		subject            string
		outdated           bool
		start, end         int
		startSide, endSide string
	}
	cases := []struct {
		name string
		th   github.Thread
		want want
	}{
		{
			name: "single right line",
			th:   github.Thread{SubjectType: "LINE", Line: 12, DiffSide: "RIGHT"},
			want: want{subjectLine, false, 12, 12, "additions", "additions"},
		},
		{
			name: "left range",
			th:   github.Thread{SubjectType: "LINE", Line: 9, StartLine: 4, DiffSide: "LEFT", StartDiffSide: "LEFT"},
			want: want{subjectLine, false, 4, 9, "deletions", "deletions"},
		},
		{
			name: "range crossing sides",
			th:   github.Thread{SubjectType: "LINE", Line: 7, StartLine: 3, DiffSide: "RIGHT", StartDiffSide: "LEFT"},
			want: want{subjectLine, false, 3, 7, "deletions", "additions"},
		},
		{
			name: "outdated keeps original lines on the file header",
			th:   github.Thread{SubjectType: "LINE", IsOutdated: true, OriginalLine: 20, OriginalStartLine: 18, DiffSide: "RIGHT"},
			want: want{subjectFile, true, 18, 20, "additions", "additions"},
		},
		{
			name: "file subject",
			th:   github.Thread{SubjectType: "FILE", DiffSide: "RIGHT"},
			want: want{subjectFile, false, 0, 0, "additions", "additions"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject, outdated, start, end, startSide, endSide := threadAnchor(tc.th)
			got := want{subject, outdated, start, end, startSide, endSide}
			if got != tc.want {
				t.Fatalf("threadAnchor = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestAuthorOf(t *testing.T) {
	p := &poller{app: ghapp.App{BotLogin: "cc-review-octo[bot]"}, viewer: "octo"}
	cases := []struct {
		name, login, body, author, origin string
	}{
		{"app bot is claude", "cc-review-octo[bot]", "<!-- pr-reviewer bot summary -->", store.AuthorClaude, ccevent.OriginAgent},
		{"viewer", "octo", "rename this", store.AuthorUser, ccevent.OriginHuman},
		{"coworker", "hubot", "why?", store.AuthorRemote, ccevent.OriginHuman},
		{"graphite merge activity", "graphite-app[bot]", "### Merge activity\n\n* Oct 7, 2:10 AM UTC: queued", store.AuthorAutomation, ccevent.OriginAgent},
		{"pr-reviewer summary", "forge-pr-reviewer[bot]", "<!-- pr-reviewer bot summary -->\nTwo findings.", store.AuthorAutomation, ccevent.OriginAgent},
		{"pr-reviewer companion", "forge-pr-reviewer[bot]", "<!-- pr-reviewer bot companion -->\n<!-- eyJ4IjoxfQ== -->", store.AuthorAutomation, ccevent.OriginAgent},
		{"github actions", "github-actions[bot]", "Deployed preview.", store.AuthorAutomation, ccevent.OriginAgent},
		{"any bot", "dependabot[bot]", "Bumps x from 1 to 2.", store.AuthorAutomation, ccevent.OriginAgent},
		{"graphite stack comment under the viewer", "octo", "<!-- Current dependencies on/for this PR: -->\n* **#2**", store.AuthorAutomation, ccevent.OriginAgent},
		{"graphite stack footer under the viewer", "octo", "This stack of pull requests is managed by Graphite.", store.AuthorAutomation, ccevent.OriginAgent},
		{"pr-reviewer companion under a human", "hubot", "<!-- pr-reviewer bot companion -->", store.AuthorAutomation, ccevent.OriginAgent},
		{"ci-timing from a service account", "poetic-svc", "<!-- ci-timing -->\n🐌 sandsql-parity took 14m", store.AuthorAutomation, ccevent.OriginAgent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			author, origin := p.authorOf(tc.login, tc.body)
			if author != tc.author || origin != tc.origin {
				t.Fatalf("authorOf(%q, %q) = (%q, %q), want (%q, %q)", tc.login, tc.body, author, origin, tc.author, tc.origin)
			}
		})
	}
}
