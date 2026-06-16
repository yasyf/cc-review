package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yasyf/cc-review/internal/daemon"
	"github.com/yasyf/cc-review/internal/store"
)

func newReplyCmd() *cobra.Command {
	var (
		comment     int64
		kind        string
		body        string
		header      string
		multiSelect bool
		optionsJSON string
		answer      string
		selects     []string
		other       string
		notes       string
		answerTo    int64
		session     string
		cwd         string
	)
	cmd := &cobra.Command{
		Use:   "reply",
		Short: "Post a Claude question/ask/clarification under a comment, or answer one",
		Long: "reply persists a Claude reply and pushes it under the matching comment in realtime.\n" +
			"It returns immediately (no blocking). Use --comment with --kind/--body to post a new\n" +
			"reply (--kind ask adds --header/--multi-select/--options-json), or --answer-to to\n" +
			"record an answer: --select/--other/--notes for an ask, --answer for a plain question.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if answerTo == 0 && comment == 0 {
				return errors.New("reply requires --comment (new reply) or --answer-to (answer a question)")
			}
			// Cross-reject flags from the other branch: a flag that would be silently
			// dropped is an error, not a no-op.
			changed := func(names ...string) []string {
				var hit []string
				for _, n := range names {
					if cmd.Flags().Changed(n) {
						hit = append(hit, "--"+n)
					}
				}
				return hit
			}
			in := daemon.ReplyInput{CommentID: comment, AnswerTo: answerTo}
			if answerTo != 0 {
				if hit := changed("kind", "body", "header", "multi-select", "options-json"); len(hit) > 0 {
					return fmt.Errorf("%s post a new reply; --answer-to answers an existing one", strings.Join(hit, "/"))
				}
				if len(selects) > 0 || other != "" || notes != "" {
					if answer != "" {
						return errors.New("--answer is for plain questions; --select/--other/--notes answer an ask")
					}
					in.AskAnswer = &store.AskAnswer{Selected: selects, Other: other, Notes: notes}
				} else if answer != "" {
					in.Answer = answer
				} else {
					return errors.New("--answer-to requires --select/--other/--notes (ask) or --answer (question)")
				}
			} else {
				if hit := changed("select", "other", "notes", "answer"); len(hit) > 0 {
					return fmt.Errorf("%s answer an existing reply; use --answer-to", strings.Join(hit, "/"))
				}
				in.Kind = kind
				in.Body = body
				if kind == "ask" != (optionsJSON != "") {
					return errors.New("--options-json and --kind ask go together")
				}
				if optionsJSON != "" {
					var options []store.AskOption
					if err := json.Unmarshal([]byte(optionsJSON), &options); err != nil {
						return fmt.Errorf("parse --options-json: %w", err)
					}
					in.Ask = &store.Ask{Header: header, MultiSelect: multiSelect, Options: options}
				} else if header != "" || multiSelect {
					return errors.New("--header/--multi-select require --kind ask")
				}
			}
			ctx := cmd.Context()
			if err := ensureCurrent(ctx); err != nil {
				return err
			}
			return daemon.NewReviewClient().Reply(ctx, session, mustCwd(cwd), []daemon.ReplyInput{in})
		},
	}
	cmd.Flags().Int64Var(&comment, "comment", 0, "comment id to reply under")
	cmd.Flags().StringVar(&kind, "kind", "clarification", "reply kind: question | ask | clarification")
	cmd.Flags().StringVar(&body, "body", "", "reply text (the question for --kind ask)")
	cmd.Flags().StringVar(&header, "header", "", "short chip shown on the ask card, e.g. Approach")
	cmd.Flags().BoolVar(&multiSelect, "multi-select", false, "allow picking multiple options")
	cmd.Flags().StringVar(&optionsJSON, "options-json", "", `JSON array of {"label","description"?,"preview"?} (requires --kind ask)`)
	cmd.Flags().StringVar(&answer, "answer", "", "the answer text when answering a plain question")
	cmd.Flags().StringArrayVar(&selects, "select", nil, "a chosen option label when answering an ask (repeatable)")
	cmd.Flags().StringVar(&other, "other", "", "free-text answer outside the offered options")
	cmd.Flags().StringVar(&notes, "notes", "", "a note riding along with the selection")
	cmd.Flags().Int64Var(&answerTo, "answer-to", 0, "the reply id of the question or ask being answered")
	cmd.Flags().StringVar(&session, "session", "", "Claude session id")
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory (defaults to the current directory)")
	return cmd
}
