import { useState } from 'react';
import { useCreateReply } from '../lib/api';
import { clearAskDraft, readAskDraft, writeAskDraft } from '../lib/drafts';
import type { Reply } from '../lib/types';

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
  // Rehydrate across portal remounts (virtualizer releases the file's item
  // when it scrolls far off screen); submit clears the stored draft.
  const draft = readAskDraft(reply.id);
  const [selected, setSelected] = useState<string[]>(() => draft?.selected ?? []);
  const [otherChosen, setOtherChosen] = useState(() => draft?.otherChosen ?? false);
  const [otherText, setOtherText] = useState(() => draft?.otherText ?? '');
  const [notes, setNotes] = useState(() => draft?.notes ?? '');
  const [focusedLabel, setFocusedLabel] = useState<string | null>(null);

  const { ask } = reply;
  const multiSelect = ask.multiSelect === true;

  function persist(next: Partial<Parameters<typeof writeAskDraft>[1]>) {
    writeAskDraft(reply.id, { selected, otherChosen, otherText, notes, ...next });
  }

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

  const hasPreview = ask.options.some((option) => option.preview !== undefined);
  const focusedOption =
    focusedLabel === null ? undefined : ask.options.find((option) => option.label === focusedLabel);
  const preview =
    focusedOption === undefined
      ? ask.options.find((option) => option.preview !== undefined)?.preview
      : focusedOption.preview;

  const trimmedOther = otherText.trim();
  const canSubmit = selected.length > 0 || (otherChosen && trimmedOther !== '');

  function toggleOption(label: string) {
    const next = multiSelect
      ? selected.includes(label)
        ? selected.filter((l) => l !== label)
        : [...selected, label]
      : selected.includes(label)
        ? []
        : [label];
    setSelected(next);
    if (!multiSelect) {
      setOtherChosen(false);
      persist({ selected: next, otherChosen: false });
    } else {
      persist({ selected: next });
    }
  }

  function toggleOther() {
    const next = !otherChosen;
    setOtherChosen(next);
    if (!multiSelect) {
      setSelected([]);
      persist({ otherChosen: next, selected: [] });
    } else {
      persist({ otherChosen: next });
    }
  }

  function updateOtherText(text: string) {
    setOtherText(text);
    persist({ otherText: text });
  }

  function updateNotes(text: string) {
    setNotes(text);
    persist({ notes: text });
  }

  function submit() {
    const note = notes.trim();
    clearAskDraft(reply.id);
    createReply.mutate({
      commentId,
      askAnswer: {
        selected,
        ...(otherChosen && trimmedOther !== '' ? { other: trimmedOther } : {}),
        ...(note !== '' ? { notes: note } : {}),
      },
      questionReplyId: reply.id,
    });
  }

  const optionRows = (
    <div className="qc-options">
      {ask.options.map((option) => {
        const isSelected = selected.includes(option.label);
        const isFocused = focusedLabel === option.label;
        return (
          <button
            key={option.label}
            type="button"
            className={`qc-option${isSelected ? ' qc-option-selected' : ''}${isFocused ? ' qc-option-focused' : ''}`}
            disabled={disabled}
            aria-pressed={isSelected}
            onClick={() => toggleOption(option.label)}
            onMouseEnter={() => setFocusedLabel(option.label)}
            onFocus={() => setFocusedLabel(option.label)}
          >
            <span className="qc-option-label">{option.label}</span>
            {option.description ? <span className="qc-option-desc">{option.description}</span> : null}
          </button>
        );
      })}
      <button
        type="button"
        className={`qc-option${otherChosen ? ' qc-option-selected' : ''}`}
        disabled={disabled}
        aria-pressed={otherChosen}
        onClick={toggleOther}
      >
        <span className="qc-option-label">Other</span>
      </button>
      {otherChosen ? (
        <input
          type="text"
          className="qc-other-input"
          value={otherText}
          placeholder="Your answer…"
          disabled={disabled}
          onChange={(e) => updateOtherText(e.target.value)}
        />
      ) : null}
    </div>
  );

  return (
    <div className="question-card">
      {ask.header ? <div className="qc-chip">{ask.header}</div> : null}
      <div className="reply-body">{reply.body}</div>
      {hasPreview ? (
        <div className="qc-grid">
          {optionRows}
          <pre className={`qc-preview${preview === undefined ? ' qc-preview-empty' : ''}`}>
            {preview ?? 'no preview'}
          </pre>
        </div>
      ) : (
        optionRows
      )}
      <textarea
        className="qc-notes"
        value={notes}
        placeholder="Notes (optional)"
        disabled={disabled}
        onChange={(e) => updateNotes(e.target.value)}
      />
      {disabled ? (
        <div className="qc-hint">Review submitted — Claude will ask this directly.</div>
      ) : (
        <div className="qc-actions">
          <button
            type="button"
            className="primary"
            disabled={!canSubmit || createReply.isPending}
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
