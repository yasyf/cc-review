package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/yasyf/cc-review/internal/daemon"
)

func newReplyCmd() *cobra.Command {
	var (
		comment  int64
		kind     string
		body     string
		options  []string
		answer   string
		answerTo int64
	)
	cmd := &cobra.Command{
		Use:   "reply",
		Short: "Post a Claude question/option/clarification under a comment, or answer a question",
		Long: "reply persists a Claude reply and pushes it under the matching comment in realtime.\n" +
			"It returns immediately (no blocking). Use --comment with --kind/--body/--option to post\n" +
			"a new reply, or --answer-to with --answer to record an answer to a Claude question.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if answerTo == 0 && comment == 0 {
				return errors.New("reply requires --comment (new reply) or --answer-to (answer a question)")
			}
			if err := daemon.EnsureCurrent(daemon.UpgradeTimeout); err != nil {
				return err
			}
			resp, err := daemon.NewClient().Reply([]daemon.ReplyInput{{
				CommentID: comment, Kind: kind, Body: body, Options: options, Answer: answer, AnswerTo: answerTo,
			}})
			if err != nil {
				return err
			}
			if !resp.OK {
				return errors.New(resp.Error)
			}
			return nil
		},
	}
	cmd.Flags().Int64Var(&comment, "comment", 0, "comment id to reply under")
	cmd.Flags().StringVar(&kind, "kind", "clarification", "reply kind: question | option | clarification")
	cmd.Flags().StringVar(&body, "body", "", "reply text")
	cmd.Flags().StringArrayVar(&options, "option", nil, "an option to offer (repeatable; implies --kind option)")
	cmd.Flags().StringVar(&answer, "answer", "", "the answer text when answering a question")
	cmd.Flags().Int64Var(&answerTo, "answer-to", 0, "the reply id of the question being answered")
	return cmd
}
