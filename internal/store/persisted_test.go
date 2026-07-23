package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestPersistedSchemaFingerprintsPinned(t *testing.T) {
	for _, schema := range []struct {
		identity, descriptor, fingerprint string
	}{
		{unmatchedSchemaIdentity, unmatchedSchemaDescriptor, unmatchedSchemaFingerprint},
		{changesSchemaIdentity, changesSchemaDescriptor, changesSchemaFingerprint},
		{questionSchemaIdentity, questionSchemaDescriptor, questionSchemaFingerprint},
		{answerSchemaIdentity, answerSchemaDescriptor, answerSchemaFingerprint},
		{replyAskSchemaIdentity, replyAskSchemaDescriptor, replyAskSchemaFingerprint},
		{replyAskAnswerSchemaIdentity, replyAskAnswerSchemaDescriptor, replyAskAnswerSchemaFingerprint},
		{attributionSchemaIdentity, attributionSchemaDescriptor, attributionSchemaFingerprint},
	} {
		digest := sha256.Sum256([]byte(schema.identity + "\x00v1\x00" + schema.descriptor))
		want := schema.identity + "." + hex.EncodeToString(digest[:])
		if schema.fingerprint != want {
			t.Errorf("%s fingerprint = %q, want %q", schema.identity, schema.fingerprint, want)
		}
	}
}

func TestPersistedCodecsRejectNonExactEnvelopes(t *testing.T) {
	unmatched, _ := encodeUnmatched([]Unmatched{{Pattern: "p", Why: "w"}})
	changes, _ := encodeChanges([]AIChange{{
		Path: "a.go", Reason: "r", Prior: PriorState{Fingerprint: "fp"}, Applied: AppliedState{Reviewed: true},
	}})
	question, _ := encodeQuestion(AIQuestion{Body: "q", Ask: &Ask{Options: []AskOption{{Label: "A"}}}})
	answer, _ := encodeAnswer(AIAnswer{AskAnswer: &AskAnswer{Selected: []string{"A"}}})
	ask, _ := encodeReplyAsk(Ask{Options: []AskOption{{Label: "A"}}})
	askAnswer, _ := encodeReplyAskAnswer(AskAnswer{Selected: []string{"A"}})
	attributions, _ := encodeAttributionRanges([]AttributionRange{{Start: 1, End: 2, TurnID: 3}})

	for _, codec := range []struct {
		name, identity, fingerprint, valid string
		decode                             func(string) error
	}{
		{"unmatched", unmatchedSchemaIdentity, unmatchedSchemaFingerprint, unmatched, func(value string) error { _, err := decodeUnmatched(value); return err }},
		{"changes", changesSchemaIdentity, changesSchemaFingerprint, changes, func(value string) error { _, err := decodeChanges(value); return err }},
		{"question", questionSchemaIdentity, questionSchemaFingerprint, question, func(value string) error { _, err := decodeQuestion(value); return err }},
		{"answer", answerSchemaIdentity, answerSchemaFingerprint, answer, func(value string) error { _, err := decodeAnswer(value); return err }},
		{"reply ask", replyAskSchemaIdentity, replyAskSchemaFingerprint, ask, func(value string) error { _, err := decodeReplyAsk(value); return err }},
		{"reply ask answer", replyAskAnswerSchemaIdentity, replyAskAnswerSchemaFingerprint, askAnswer, func(value string) error { _, err := decodeReplyAskAnswer(value); return err }},
		{"attributions", attributionSchemaIdentity, attributionSchemaFingerprint, attributions, func(value string) error { _, err := decodeAttributionRanges(value); return err }},
	} {
		t.Run(codec.name, func(t *testing.T) {
			payloadIndex := strings.Index(codec.valid, `"payload":`)
			for _, broken := range []string{
				`{`,
				`[]`,
				strings.Replace(codec.valid, codec.identity, "dev.yasyf.foreign", 1),
				strings.Replace(codec.valid, codec.fingerprint, codec.identity+".stale", 1),
				strings.Replace(codec.valid, `"schemaVersion":1`, `"schemaVersion":2`, 1),
				strings.Replace(codec.valid, `"schema":"`+codec.identity+`",`, "", 1),
				codec.valid[:payloadIndex] + `"payload":null}`,
				strings.TrimSuffix(codec.valid, "}") + `,"legacy":true}`,
				codec.valid + ` {}`,
			} {
				if err := codec.decode(broken); err == nil {
					t.Fatalf("decoder accepted %s", broken)
				}
			}
		})
	}

	for _, tc := range []struct {
		name, value string
		decode      func(string) error
	}{
		{"unmatched missing why", strings.Replace(unmatched, `,"why":"w"`, "", 1), func(value string) error { _, err := decodeUnmatched(value); return err }},
		{"changes unknown", strings.Replace(changes, `"reason":"r"`, `"reason":"r","extra":true`, 1), func(value string) error { _, err := decodeChanges(value); return err }},
		{"question missing ask", strings.Replace(question, `,"ask":`, `,"missing":`, 1), func(value string) error { _, err := decodeQuestion(value); return err }},
		{"answer null selected", strings.Replace(answer, `"selected":["A"]`, `"selected":null`, 1), func(value string) error { _, err := decodeAnswer(value); return err }},
		{"ask missing option description", strings.Replace(ask, `,"description":""`, "", 1), func(value string) error { _, err := decodeReplyAsk(value); return err }},
		{"ask answer missing notes", strings.Replace(askAnswer, `,"notes":""`, "", 1), func(value string) error { _, err := decodeReplyAskAnswer(value); return err }},
		{"attribution missing turn", strings.Replace(attributions, `,"turnId":3`, "", 1), func(value string) error { _, err := decodeAttributionRanges(value); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.decode(tc.value); err == nil {
				t.Fatalf("decoder accepted %s", tc.value)
			}
		})
	}
}

func TestAIRequestJSONColumnsAreExact(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	reviewID := seedReview(ctx, t, s, "s", 0, "/repo", "main", "base0")
	request, err := s.CreateAIRequest(ctx, reviewID, 1, "user", "review it")
	if err != nil {
		t.Fatal(err)
	}
	var unmatched, changes string
	if err := s.db.QueryRowContext(ctx, `SELECT unmatched_json, changes_json FROM ai_requests WHERE id=?`, request.ID).
		Scan(&unmatched, &changes); err != nil {
		t.Fatal(err)
	}
	wantUnmatched := `{"schema":"` + unmatchedSchemaIdentity + `","schemaVersion":1,"schemaFingerprint":"` + unmatchedSchemaFingerprint + `","payload":[]}`
	wantChanges := `{"schema":"` + changesSchemaIdentity + `","schemaVersion":1,"schemaFingerprint":"` + changesSchemaFingerprint + `","payload":[]}`
	if unmatched != wantUnmatched || changes != wantChanges {
		t.Fatalf("initial JSON columns = unmatched:%s changes:%s", unmatched, changes)
	}
	for _, tc := range []struct {
		name, value string
	}{
		{"legacy", `[]`},
		{"foreign", strings.Replace(wantUnmatched, unmatchedSchemaIdentity, "dev.yasyf.foreign", 1)},
		{"wrong fingerprint", strings.Replace(wantUnmatched, unmatchedSchemaFingerprint, unmatchedSchemaIdentity+".stale", 1)},
		{"wrong version", strings.Replace(wantUnmatched, `"schemaVersion":1`, `"schemaVersion":2`, 1)},
		{"missing schema", strings.Replace(wantUnmatched, `"schema":"`+unmatchedSchemaIdentity+`",`, "", 1)},
		{"null payload", strings.Replace(wantUnmatched, `"payload":[]`, `"payload":null`, 1)},
		{"unknown payload field", strings.Replace(wantUnmatched, `"payload":[]`, `"payload":[{"pattern":"x","why":"y","extra":true}]`, 1)},
		{"missing payload field", strings.Replace(wantUnmatched, `"payload":[]`, `"payload":[{"pattern":"x"}]`, 1)},
		{"trailing", wantUnmatched + ` {}`},
		{"corrupt", `{`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.db.ExecContext(ctx, `UPDATE ai_requests SET unmatched_json=? WHERE id=?`, tc.value, request.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetAIRequest(ctx, request.ID); err == nil {
				t.Fatalf("GetAIRequest accepted %s", tc.value)
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE ai_requests SET unmatched_json=? WHERE id=?`, wantUnmatched, request.ID); err != nil {
				t.Fatal(err)
			}
		})
	}

	if _, err := s.AskAIRequest(ctx, request.ID, AIQuestion{
		Body: "Which?", Ask: &Ask{Header: "Choice", Options: []AskOption{{Label: "A"}}},
	}); err != nil {
		t.Fatal(err)
	}
	var question string
	if err := s.db.QueryRowContext(ctx, `SELECT question_json FROM ai_requests WHERE id=?`, request.ID).Scan(&question); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(question, `"schema":"`+questionSchemaIdentity+`"`) ||
		!strings.Contains(question, `"ask":{"header":"Choice","multiSelect":false,"options":[{"label":"A","description":"","preview":""}]}`) {
		t.Fatalf("question_json = %s", question)
	}
	if _, err := s.AnswerAIRequest(ctx, request.ID, AIAnswer{AskAnswer: &AskAnswer{Selected: []string{"A"}}}); err != nil {
		t.Fatal(err)
	}
	var answer string
	if err := s.db.QueryRowContext(ctx, `SELECT answer_json FROM ai_requests WHERE id=?`, request.ID).Scan(&answer); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(answer, `"schema":"`+answerSchemaIdentity+`"`) ||
		!strings.Contains(answer, `"payload":{"text":"","askAnswer":{"selected":["A"],"other":"","notes":""}}`) {
		t.Fatalf("answer_json = %s", answer)
	}
}

func TestReplyJSONColumnsAreExact(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	reviewID := seedReview(ctx, t, s, "s", 0, "/repo", "main", "base0")
	version, err := s.CreateVersion(ctx, reviewID, "main", "HEAD", "/p", "[]", "")
	if err != nil {
		t.Fatal(err)
	}
	commentID, err := s.CreateComment(ctx, Comment{VersionID: version.ID, FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1})
	if err != nil {
		t.Fatal(err)
	}
	replyID, _, err := s.CreateReply(ctx, Reply{
		CommentID: commentID, Origin: "claude", Kind: "ask", Body: "choose",
		Ask: &Ask{Header: "Choice", Options: []AskOption{{Label: "A"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var ask string
	var answer any
	if err := s.db.QueryRowContext(ctx, `SELECT ask_json, ask_answer_json FROM replies WHERE id=?`, replyID).Scan(&ask, &answer); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ask, `"schema":"`+replyAskSchemaIdentity+`"`) || answer != nil {
		t.Fatalf("ask_json=%s ask_answer_json=%v", ask, answer)
	}
	if err := s.AnswerAsk(ctx, replyID, AskAnswer{Selected: []string{"A"}}, "web"); err != nil {
		t.Fatal(err)
	}
	var askAnswer string
	if err := s.db.QueryRowContext(ctx, `SELECT ask_answer_json FROM replies WHERE id=?`, replyID).Scan(&askAnswer); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(askAnswer, `"schema":"`+replyAskAnswerSchemaIdentity+`"`) {
		t.Fatalf("ask_answer_json=%s", askAnswer)
	}

	if _, err := s.db.ExecContext(ctx, `UPDATE replies SET ask_json=? WHERE id=?`, `{"schema":"foreign"}`, replyID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetReply(ctx, replyID); err == nil {
		t.Fatal("GetReply accepted corrupt ask_json")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE replies SET ask_json=?, ask_answer_json=? WHERE id=?`, ask, askAnswer+` {}`, replyID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetReply(ctx, replyID); err == nil {
		t.Fatal("GetReply accepted trailing ask_answer_json")
	}
}

func TestAttributionJSONColumnIsExact(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	reviewID := seedReview(ctx, t, s, "s", 0, "/repo", "main", "base0")
	version, err := s.CreateVersion(ctx, reviewID, "main", "HEAD", "/p", "[]", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutAttributions(ctx, version.ID, map[string][]AttributionRange{
		"a.go": {{Start: 1, End: 2, TurnID: 3}},
	}); err != nil {
		t.Fatal(err)
	}
	var ranges string
	if err := s.db.QueryRowContext(ctx, `SELECT ranges_json FROM turn_attributions WHERE version_id=? AND file_path='a.go'`, version.ID).
		Scan(&ranges); err != nil {
		t.Fatal(err)
	}
	want := `{"schema":"` + attributionSchemaIdentity + `","schemaVersion":1,"schemaFingerprint":"` + attributionSchemaFingerprint + `","payload":[{"start":1,"end":2,"turnId":3}]}`
	if ranges != want {
		t.Fatalf("ranges_json=%s, want %s", ranges, want)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE turn_attributions SET ranges_json=? WHERE version_id=? AND file_path='a.go'`, strings.Replace(want, `"end":2`, `"end":2,"extra":true`, 1), version.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListAttributionsByVersion(ctx, version.ID); err == nil {
		t.Fatal("ListAttributionsByVersion accepted an extended ranges row")
	}
}
