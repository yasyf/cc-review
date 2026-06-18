package corrections

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yasyf/cc-review/internal/feedback"
)

func TestArgsPerThread(t *testing.T) {
	ts := time.UnixMilli(1718000000000)
	cases := []struct {
		id       string
		reviewID string
		thread   feedback.Thread
		session  string
		repo     string
		want     []string
	}{
		{
			id:       "anchors comment and stamps null-digest review row",
			reviewID: "rev1",
			thread:   feedback.Thread{CommentID: 42, FilePath: "internal/foo.go", LineContent: "x := bad()", Body: "use good()"},
			session:  "sess-abc",
			repo:     "/repo",
			want: []string{
				"corrections", "add",
				"--session", "sess-abc",
				"--source", "cc-review",
				"--anchor", "review:rev1:42",
				"--origin", "review",
				"--incorrect-file", "internal/foo.go",
				"--incorrect-new", "x := bad()",
				"--correction-text", "use good()",
				"--repo", "/repo",
				"--ts-ms", "1718000000000",
			},
		},
		{
			id:       "empty line content and body pass through verbatim",
			reviewID: "abcd1234",
			thread:   feedback.Thread{CommentID: 7, FilePath: "a.go", LineContent: "", Body: ""},
			session:  "s2",
			repo:     "/other/repo",
			want: []string{
				"corrections", "add",
				"--session", "s2",
				"--source", "cc-review",
				"--anchor", "review:abcd1234:7",
				"--origin", "review",
				"--incorrect-file", "a.go",
				"--incorrect-new", "",
				"--correction-text", "",
				"--repo", "/other/repo",
				"--ts-ms", "1718000000000",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			got := args(tc.reviewID, tc.thread, tc.session, tc.repo, ts)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("args =\n  %#v\nwant\n  %#v", got, tc.want)
			}
		})
	}
}

func TestBinResolvesFromEnv(t *testing.T) {
	t.Setenv(binEnv, "")
	if got := Bin(); got != defaultBin {
		t.Fatalf("Bin() with empty env = %q, want %q", got, defaultBin)
	}
	t.Setenv(binEnv, "/dev/cc-transcript")
	if got := Bin(); got != "/dev/cc-transcript" {
		t.Fatalf("Bin() = %q, want the env override", got)
	}
}

// recordingBin writes a shell stub to CC_TRANSCRIPT_BIN that appends its full
// argv (one line per invocation, tab-separated) to a log file, then returns the
// log path so the test can read back exactly what Write shelled out.
func recordingBin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	stub := filepath.Join(dir, "cc-transcript")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + log + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(binEnv, stub)
	return log
}

func TestWriteShellsOutOncePerThread(t *testing.T) {
	log := recordingBin(t)
	fb := feedback.Feedback{
		ReviewID:  "rev9",
		SessionID: "sess-xyz",
		Threads: []feedback.Thread{
			{CommentID: 1, FilePath: "a.go", LineContent: "bad1", Body: "fix1"},
			{CommentID: 2, FilePath: "b.go", LineContent: "bad2", Body: "fix2"},
		},
	}
	if err := Write(context.Background(), fb, "/repo", time.UnixMilli(1718000000000)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	lines := nonEmptyLines(t, log)
	if len(lines) != 2 {
		t.Fatalf("invocations = %d, want one per thread (2): %q", len(lines), lines)
	}
	for i, want := range []string{"--anchor review:rev9:1", "--anchor review:rev9:2"} {
		if !strings.Contains(lines[i], want) {
			t.Fatalf("invocation %d = %q, want it to contain %q", i, lines[i], want)
		}
	}
	if !strings.Contains(lines[0], "--session sess-xyz") || !strings.Contains(lines[0], "--repo /repo") {
		t.Fatalf("invocation 0 = %q, missing session/repo", lines[0])
	}
}

func TestWriteRejectsMissingSession(t *testing.T) {
	log := recordingBin(t)
	fb := feedback.Feedback{ReviewID: "rev9", Threads: []feedback.Thread{{CommentID: 1, FilePath: "a.go"}}}
	if err := Write(context.Background(), fb, "/repo", time.Now()); err == nil {
		t.Fatal("Write with no session id should error")
	}
	if lines := nonEmptyLines(t, log); len(lines) != 0 {
		t.Fatalf("no shell-out should happen without a session, got %q", lines)
	}
}

func TestWriteAggregatesThreadFailures(t *testing.T) {
	t.Setenv(binEnv, "/nonexistent/cc-transcript-binary")
	fb := feedback.Feedback{
		ReviewID:  "rev9",
		SessionID: "sess-xyz",
		Threads: []feedback.Thread{
			{CommentID: 1, FilePath: "a.go"},
			{CommentID: 2, FilePath: "b.go"},
		},
	}
	err := Write(context.Background(), fb, "/repo", time.Now())
	if err == nil {
		t.Fatal("Write against a missing binary should error")
	}
	for _, want := range []string{"comment 1", "comment 2"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should name failing %s", err, want)
		}
	}
}

func nonEmptyLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}
