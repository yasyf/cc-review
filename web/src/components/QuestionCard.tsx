import { useCreateReply } from '../lib/api';
import { useAskAnswer } from '../lib/ask-answer';
import type { Reply } from '../lib/types';
import { AskOptionPicker } from './AskOptionPicker';

type AskReply = Extract<Reply, { kind: 'ask' }>;

export function QuestionCard({
  reply,
  commentId,
  disabled,
}: {
  reply: AskReply;
  commentId: string;
  disabled: boolean;
}) {
  const createReply = useCreateReply();
  const { ask } = reply;
  // Draft survives portal remounts (the virtualizer releases a file's item when it
  // scrolls far off screen); submit clears it.
  const form = useAskAnswer(ask, reply.id);

  if (reply.answered) {
    const answer = reply.askAnswer;
    return (
      <div className="question-card qc-answered">
        {ask.header ? <div className="qc-chip">{ask.header}</div> : null}
        <div className="reply-body">{reply.body}</div>
        <div className="qc-options">
          {ask.options.map((option) => (
            <div
              key={option.label}
              className={`qc-option${answer?.selected.includes(option.label) ? ' qc-option-selected' : ''}`}
            >
              <span className="qc-option-label">{option.label}</span>
              {option.description ? <span className="qc-option-desc">{option.description}</span> : null}
            </div>
          ))}
          {answer?.other ? (
            <div className="qc-option qc-option-selected">
              <span className="qc-option-label">Other</span>
              <span className="qc-option-desc">{answer.other}</span>
            </div>
          ) : null}
        </div>
        {answer?.notes ? <div className="qc-notes-text">{answer.notes}</div> : null}
        <div className="qc-meta">
          Answered via {reply.answeredVia === 'askuserquestion' ? 'AskUserQuestion' : 'web'}
        </div>
      </div>
    );
  }

  function submit() {
    form.clear();
    createReply.mutate({ commentId, askAnswer: form.answer, questionReplyId: reply.id });
  }

  return (
    <div className="question-card">
      {ask.header ? <div className="qc-chip">{ask.header}</div> : null}
      <div className="reply-body">{reply.body}</div>
      <AskOptionPicker
        options={ask.options}
        selected={form.selected}
        otherChosen={form.otherChosen}
        otherText={form.otherText}
        focusedLabel={form.focusedLabel}
        disabled={disabled}
        onToggle={form.toggleOption}
        onToggleOther={form.toggleOther}
        onOtherText={form.updateOtherText}
        onFocusLabel={form.setFocusedLabel}
      />
      <textarea
        className="qc-notes"
        value={form.notes}
        placeholder="Notes (optional)"
        disabled={disabled}
        onChange={(e) => form.updateNotes(e.target.value)}
      />
      {disabled ? (
        <div className="qc-hint">Review submitted — Claude will ask this directly.</div>
      ) : (
        <div className="qc-actions">
          <button
            type="button"
            className="primary"
            disabled={!form.canSubmit || createReply.isPending}
            onClick={submit}
          >
            {createReply.isPending ? 'Sending…' : 'Submit'}
          </button>
          {createReply.isError ? <div className="qc-error">{createReply.error.message}</div> : null}
        </div>
      )}
    </div>
  );
}
