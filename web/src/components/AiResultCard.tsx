import { useState } from 'react';
import type { RefObject } from 'react';
import { useAnswerAiRequest, useUndoAiRequest } from '../lib/api';
import { useAskAnswer } from '../lib/ask-answer';
import { useReview } from '../lib/review-context';
import type { LocalRequest } from '../lib/local-requests';
import type { Ask, AiRequest } from '../lib/types';
import { AskOptionPicker } from './AskOptionPicker';
import type { DiffViewHandle } from './DiffView';

const STATUS_LABEL: Record<AiRequest['status'], string> = {
  pending: 'queued…',
  working: 'working…',
  awaiting_input: 'needs you',
  answered: 'resuming…',
  done: 'done',
  failed: 'failed',
  undone: 'undone',
};

// Free-text reply to a clarifying question with no structured options.
function AnswerText({ request }: { request: AiRequest }) {
  const { slug } = useReview();
  const answerRequest = useAnswerAiRequest(slug);
  const [text, setText] = useState('');
  const canSubmit = text.trim() !== '';

  function submit() {
    if (!canSubmit || answerRequest.isPending) return;
    answerRequest.mutate({ id: request.id, answer: text.trim() });
  }

  return (
    <div className="question-card">
      <div className="reply-body">{request.question?.body}</div>
      <input
        type="text"
        value={text}
        placeholder="Your answer…"
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault();
            submit();
          }
        }}
      />
      <div className="qc-actions">
        <button type="button" className="primary" disabled={!canSubmit || answerRequest.isPending} onClick={submit}>
          {answerRequest.isPending ? 'Sending…' : 'Answer'}
        </button>
        {answerRequest.isError ? <div className="qc-error">{answerRequest.error.message}</div> : null}
      </div>
    </div>
  );
}

// Structured reply, reusing the same option picker + selection logic as the
// comment-thread QuestionCard.
function AnswerAsk({ request, ask }: { request: AiRequest; ask: Ask }) {
  const { slug } = useReview();
  const answerRequest = useAnswerAiRequest(slug);
  const form = useAskAnswer(ask, `ai:${request.id}`);

  function submit() {
    if (!form.canSubmit || answerRequest.isPending) return;
    form.clear();
    answerRequest.mutate({ id: request.id, askAnswer: form.answer });
  }

  return (
    <div className="question-card">
      {ask.header ? <div className="qc-chip">{ask.header}</div> : null}
      <div className="reply-body">{request.question?.body}</div>
      <AskOptionPicker
        options={ask.options}
        selected={form.selected}
        otherChosen={form.otherChosen}
        otherText={form.otherText}
        focusedLabel={form.focusedLabel}
        disabled={false}
        onToggle={form.toggleOption}
        onToggleOther={form.toggleOther}
        onOtherText={form.updateOtherText}
        onFocusLabel={form.setFocusedLabel}
      />
      <div className="qc-actions">
        <button type="button" className="primary" disabled={!form.canSubmit || answerRequest.isPending} onClick={submit}>
          {answerRequest.isPending ? 'Sending…' : 'Answer'}
        </button>
        {answerRequest.isError ? <div className="qc-error">{answerRequest.error.message}</div> : null}
      </div>
    </div>
  );
}

export function AiResultCard({
  request,
  diffRef,
  onHideMatching,
}: {
  request: AiRequest;
  diffRef: RefObject<DiffViewHandle | null>;
  onHideMatching(pattern: string): void;
}) {
  const { slug } = useReview();
  const undoRequest = useUndoAiRequest(slug);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const inFlight = request.status === 'pending' || request.status === 'working' || request.status === 'answered';
  const phase = request.status === 'working' ? request.phase?.trim() : '';

  return (
    <div className={`ai-request ai-request-${request.status}`}>
      <div className="ai-request-line">
        <span className={`ai-status${inFlight ? ' ai-pulse' : ''}`}>{STATUS_LABEL[request.status]}</span>
        <span className="ai-prompt" title={request.prompt}>
          {request.prompt}
        </span>
        {request.status === 'done' && request.changes.length > 0 ? (
          <>
            <button type="button" className="ai-mini" onClick={() => setDetailsOpen(!detailsOpen)}>
              {detailsOpen ? 'Hide' : 'Show'} {request.changes.length} files
            </button>
            <button
              type="button"
              className="ai-mini"
              disabled={undoRequest.isPending}
              onClick={() => undoRequest.mutate(request.id)}
            >
              Undo
            </button>
          </>
        ) : null}
      </div>
      {inFlight ? <div className="ai-progress" aria-hidden /> : null}
      {phase ? <div className="ai-phase">{phase}</div> : null}
      {request.summary ? (
        <div className={`ai-summary${request.status === 'failed' ? ' ai-failed' : ''}`}>{request.summary}</div>
      ) : null}
      {detailsOpen ? (
        <ul className="ai-changes">
          {request.changes.map((change) => (
            <li key={change.path}>
              <button
                type="button"
                className="ai-changelink"
                onClick={() =>
                  diffRef.current?.scrollToFile({ sectionKey: change.sectionKey, path: change.path })
                }
              >
                <code>{change.path}</code>
              </button>{' '}
              — {change.reason}
            </li>
          ))}
        </ul>
      ) : null}
      {request.unmatched.length > 0 ? (
        <ul className="ai-unmatched">
          {request.unmatched.map((entry) => (
            <li key={entry.pattern}>
              <strong>{entry.pattern}</strong> — {entry.why}
              <button type="button" className="ai-mini" onClick={() => onHideMatching(entry.pattern)}>
                hide matching ⚡
              </button>
            </li>
          ))}
        </ul>
      ) : null}
      {request.status === 'awaiting_input' && request.question ? (
        request.question.ask ? (
          <AnswerAsk request={request} ask={request.question.ask} />
        ) : (
          <AnswerText request={request} />
        )
      ) : null}
    </div>
  );
}

// A ⚡ instant edit: applied client-side, undoable from its captured prior.
export function LocalResultCard({ request, onUndo }: { request: LocalRequest; onUndo(): void }) {
  return (
    <div className="ai-request ai-request-local">
      <div className="ai-request-line">
        <span className="ai-status ai-status-instant">⚡ done</span>
        <span className="ai-prompt" title={request.label}>
          {request.label}
        </span>
        <button type="button" className="ai-mini" onClick={onUndo}>
          Undo
        </button>
      </div>
    </div>
  );
}
