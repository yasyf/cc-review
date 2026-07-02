package cli

import (
	"reflect"
	"testing"
	"time"

	"github.com/yasyf/cc-review/internal/daemon"
)

func TestCloseLines(t *testing.T) {
	now := time.Unix(1_750_000_000, 0)
	for _, tc := range []struct {
		name   string
		closed []daemon.ReviewInfo
		stale  bool
		want   []string
	}{
		{
			name:  "empty stale sweep",
			stale: true,
			want:  []string{"nothing stale"},
		},
		{
			name:   "deliberate close",
			closed: []daemon.ReviewInfo{{Slug: "main--abc12345", Status: "closed"}},
			want:   []string{"closed main--abc12345"},
		},
		{
			name:  "stale sweep",
			stale: true,
			closed: []daemon.ReviewInfo{
				{Slug: "main--abc12345", Status: "closed", Scope: "/repo/a", LastActivity: now.Add(-49 * time.Hour)},
				{Slug: "fix--def67890", Status: "closed", Scope: "/repo/b", LastActivity: now.Add(-25 * time.Hour)},
			},
			want: []string{
				"closed main--abc12345  idle 2d1h  /repo/a",
				"closed fix--def67890  idle 1d1h  /repo/b",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := closeLines(tc.closed, tc.stale, now); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("closeLines = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestListLines(t *testing.T) {
	now := time.Unix(1_750_000_000, 0)
	for _, tc := range []struct {
		name    string
		reviews []daemon.ReviewInfo
		want    []string
	}{
		{
			name: "no open reviews",
			want: []string{"no open reviews"},
		},
		{
			name: "aligned rows",
			reviews: []daemon.ReviewInfo{
				{Slug: "main--abc12345", Status: "expired", Scope: "/repo/a", CreatedAt: now.Add(-30 * time.Hour), LastActivity: now.Add(-26 * time.Hour)},
				{Slug: "f--0f9e8d7c", Status: "open", Scope: "/repo/b", CreatedAt: now.Add(-90 * time.Minute), LastActivity: now.Add(-5 * time.Minute)},
			},
			want: []string{
				"SLUG            STATUS   AGE    IDLE  SCOPE",
				"main--abc12345  expired  1d6h   1d2h  /repo/a",
				"f--0f9e8d7c     open     1h30m  5m    /repo/b",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := listLines(tc.reviews, now); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("listLines =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestAge(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "0m"},
		{5 * time.Minute, "5m"},
		{90 * time.Minute, "1h30m"},
		{26 * time.Hour, "1d2h"},
		{6*24*time.Hour + 45*time.Minute, "6d0h"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			if got := age(tc.d); got != tc.want {
				t.Fatalf("age(%v) = %q, want %q", tc.d, got, tc.want)
			}
		})
	}
}
