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
		login, author, origin string
	}{
		{"cc-review-octo[bot]", store.AuthorClaude, ccevent.OriginAgent},
		{"octo", store.AuthorUser, ccevent.OriginHuman},
		{"hubot", store.AuthorRemote, ccevent.OriginHuman},
	}
	for _, tc := range cases {
		t.Run(tc.login, func(t *testing.T) {
			author, origin := p.authorOf(tc.login)
			if author != tc.author || origin != tc.origin {
				t.Fatalf("authorOf(%q) = (%q, %q), want (%q, %q)", tc.login, author, origin, tc.author, tc.origin)
			}
		})
	}
}
