package feedback

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	ccstore "github.com/yasyf/cc-interact/store"
	"github.com/yasyf/cc-review/internal/store"
)

func TestFeedbackSchemaFingerprintPinned(t *testing.T) {
	digest := sha256.Sum256([]byte(feedbackSchemaIdentity + "\x00v1\x00" + feedbackSchemaDescriptor))
	want := feedbackSchemaIdentity + "." + hex.EncodeToString(digest[:])
	if feedbackSchemaFingerprint != want {
		t.Fatalf("feedbackSchemaFingerprint = %q, want %q", feedbackSchemaFingerprint, want)
	}
}

func TestFreezeLoadExactV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feedback.json")
	want := Feedback{
		ReviewID: "review-1", Version: 2, SessionID: "session-1", FrozenAt: 123,
		Threads: []Thread{}, OpenQuestions: []OpenQuestion{},
	}
	if err := Freeze(path, want); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(path) //nolint:gosec // test-owned temporary feedback path.
	if err != nil {
		t.Fatal(err)
	}
	exact := `{"schema":"` + feedbackSchemaIdentity + `","schemaVersion":1,"schemaFingerprint":"` + feedbackSchemaFingerprint + `","payload":{"review_id":"review-1","version":2,"session_id":"session-1","frozen_at":123,"threads":[],"open_questions":[]}}`
	if string(written) != exact {
		t.Fatalf("feedback.json=%s, want %s", written, exact)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load=%+v, want %+v", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("feedback mode=%o, want 600", info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".persisted-json-") {
			t.Fatalf("temporary file leaked: %s", entry.Name())
		}
	}

	for _, tc := range []struct {
		name, data string
	}{
		{"legacy", `{"review_id":"review-1"}`},
		{"foreign", strings.Replace(exact, feedbackSchemaIdentity, "dev.yasyf.foreign", 1)},
		{"wrong version", strings.Replace(exact, `"schemaVersion":1`, `"schemaVersion":2`, 1)},
		{"wrong fingerprint", strings.Replace(exact, feedbackSchemaFingerprint, feedbackSchemaIdentity+".stale", 1)},
		{"missing schema", strings.Replace(exact, `"schema":"`+feedbackSchemaIdentity+`",`, "", 1)},
		{"null payload", `{"schema":"` + feedbackSchemaIdentity + `","schemaVersion":1,"schemaFingerprint":"` + feedbackSchemaFingerprint + `","payload":null}`},
		{"missing payload field", strings.Replace(exact, `"session_id":"session-1",`, "", 1)},
		{"null semantic array", strings.Replace(exact, `"threads":[]`, `"threads":null`, 1)},
		{"unknown envelope field", strings.TrimSuffix(exact, "}") + `,"legacy":true}`},
		{"unknown payload field", strings.Replace(exact, `"open_questions":[]`, `"open_questions":[],"legacy":true`, 1)},
		{"duplicate field", strings.Replace(exact, `"version":2`, `"version":2,"version":2`, 1)},
		{"trailing", exact + ` {}`},
		{"corrupt", `{`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatalf("Load accepted %s", tc.data)
			}
		})
	}

	if bytes.Equal(written, []byte(`{"review_id":"review-1"}`)) {
		t.Fatal("exact envelope unexpectedly equals legacy payload")
	}
}

func TestNestedFeedbackPayloadIsExact(t *testing.T) {
	want := Feedback{
		ReviewID: "r", Version: 1, SessionID: "s", FrozenAt: 1,
		Threads: []Thread{{
			CommentID: 2, FilePath: "a.go", Side: "additions", StartLine: 3, EndLine: 4,
			LineContent: "x", Body: "body", Status: "open", Replies: []Reply{{
				ID: 5, Origin: "claude", Kind: "ask", Body: "choose",
				Ask:      &store.Ask{Header: "Choice", Options: []store.AskOption{{Label: "A"}}},
				Answered: true, AskAnswer: &store.AskAnswer{Selected: []string{"A"}}, AnsweredVia: "web",
			}},
		}},
		OpenQuestions: []OpenQuestion{},
	}
	encoded, err := encodeFeedback(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeFeedback(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decodeFeedback=%+v, want %+v", got, want)
	}
	for _, broken := range []string{
		strings.Replace(string(encoded), `"line_content":"x"`, `"line_content":"x","legacy":true`, 1),
		strings.Replace(string(encoded), `"replies":[`, `"replies":null,"discard":[`, 1),
		strings.Replace(string(encoded), `"description":""`, `"description":null`, 1),
		strings.Replace(string(encoded), `"selected":["A"]`, `"selected":null`, 1),
	} {
		if _, err := decodeFeedback([]byte(broken)); err == nil {
			t.Fatalf("decodeFeedback accepted %s", broken)
		}
	}
}

func TestBuildCarriesAskShapes(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	subjectID := store.NewSlugHash()
	if _, err := ccstore.NewSubjectStore(st.DB()).
		Create(ctx, subjectID, store.ReviewSlug("main", subjectID), "s", "/repo", 0, "open"); err != nil {
		t.Fatal(err)
	}
	v, _ := st.CreateVersion(ctx, subjectID, "main", "HEAD", "/p", "[]", "sess-1")
	cid, _ := st.CreateComment(ctx, store.Comment{
		VersionID: v.ID, FilePath: "a.go", Side: "additions", StartLine: 3, EndLine: 3, Body: "hm",
	})

	ask := &store.Ask{Header: "Approach", Options: []store.AskOption{{Label: "A", Preview: "code"}, {Label: "B"}}}
	answeredID, _, _ := st.CreateReply(ctx, store.Reply{CommentID: cid, Origin: "claude", Kind: "ask", Body: "pick", Ask: ask})
	answer := store.AskAnswer{Selected: []string{"A"}, Notes: "n"}
	if err := st.AnswerAsk(ctx, answeredID, answer, "web"); err != nil {
		t.Fatal(err)
	}
	openAsk := &store.Ask{Options: []store.AskOption{{Label: "X"}, {Label: "Y"}}}
	openID, _, _ := st.CreateReply(ctx, store.Reply{CommentID: cid, Origin: "claude", Kind: "ask", Body: "still?", Ask: openAsk})

	fb, err := Build(ctx, st, subjectID, v, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if fb.SessionID != "sess-1" {
		t.Fatalf("feedback session id = %q, want the version's %q", fb.SessionID, "sess-1")
	}
	if len(fb.Threads) != 1 || len(fb.Threads[0].Replies) != 2 {
		t.Fatalf("threads = %+v, want 1 thread with 2 replies", fb.Threads)
	}
	got := fb.Threads[0].Replies[0]
	if got.ID != answeredID || got.Ask == nil || !reflect.DeepEqual(*got.Ask, *ask) {
		t.Fatalf("answered reply = %+v, want decoded ask", got)
	}
	if got.AskAnswer == nil || !reflect.DeepEqual(*got.AskAnswer, answer) || !got.Answered {
		t.Fatalf("answered reply answer = %+v, want %+v", got.AskAnswer, answer)
	}

	if len(fb.OpenQuestions) != 1 {
		t.Fatalf("open questions = %+v, want exactly the unanswered ask", fb.OpenQuestions)
	}
	oq := fb.OpenQuestions[0]
	if oq.ReplyID != openID || oq.Ask == nil || !reflect.DeepEqual(*oq.Ask, *openAsk) {
		t.Fatalf("open question = %+v, want ask %d", oq, openID)
	}

	// The frozen JSON uses the documented snake_case keys with nested ask shapes.
	b, err := json.Marshal(fb)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	openRaw := raw["open_questions"].([]any)[0].(map[string]any)
	if _, ok := openRaw["ask"]; !ok {
		t.Fatalf("open question JSON missing ask key: %s", b)
	}
	replyRaw := raw["threads"].([]any)[0].(map[string]any)["replies"].([]any)[0].(map[string]any)
	for _, key := range []string{"ask", "ask_answer"} {
		if _, ok := replyRaw[key]; !ok {
			t.Fatalf("answered reply JSON missing %s key: %s", key, b)
		}
	}
}
