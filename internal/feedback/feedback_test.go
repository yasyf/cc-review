package feedback

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	ccstore "github.com/yasyf/cc-interact/store"
	"github.com/yasyf/cc-review/internal/store"
)

func TestBuildCarriesAskShapes(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
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
