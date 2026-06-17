import { useEffect, useState } from 'react';
import { useAnswerAiRequest, useCreateAiRequest, useUndoAiRequest } from '../lib/api';
import { useEventStream } from '../lib/events';
import { useReview } from '../lib/review-context';
import type { AiRequest, SessionResponse } from '../lib/types';

const STATUS_LABEL: Record<AiRequest['status'], string> = {
  pending: 'queued…',
  working: 'working…',
  awaiting_input: 'waiting on you',
  answered: 'resuming…',
  done: 'done',
  failed: 'failed',
  undone: 'undone',
};

// A request still queued this long after submission, with Claude connected,
// most likely never reached the session; the daemon fails it shortly after.
const STALE_PENDING_MS = 60_000;

// Inline form for a request parked on a clarifying question: structured options
// when the question carries an ask, otherwise free text. Submitting answers the
// request, which the daemon redispatches to a fresh agent run.
function AnswerForm({ request }: { request: AiRequest }) {
  const { slug } = useReview();
  const answerRequest = useAnswerAiRequest(slug);
  const ask = request.question?.ask;
  const multiSelect = ask?.multiSelect === true;
  const [selected, setSelected] = useState<string[]>([]);
  const [text, setText] = useState('');

  function toggle(label: string) {
    setSelected((cur) =>
      multiSelect
        ? cur.includes(label)
          ? cur.filter((l) => l !== label)
          : [...cur, label]
        : cur.includes(label)
          ? []
          : [label],
    );
  }

  const canSubmit = ask ? selected.length > 0 : text.trim() !== '';

  function submit() {
    if (!canSubmit || answerRequest.isPending) return;
    if (ask) answerRequest.mutate({ id: request.id, askAnswer: { selected } });
    else answerRequest.mutate({ id: request.id, answer: text.trim() });
  }

  return (
    <div className="question-card">
      {ask?.header ? <div className="qc-chip">{ask.header}</div> : null}
      <div className="reply-body">{request.question?.body}</div>
      {ask ? (
        <div className="qc-options">
          {ask.options.map((option) => (
            <button
              key={option.label}
              type="button"
              className={`qc-option${selected.includes(option.label) ? ' qc-option-selected' : ''}`}
              aria-pressed={selected.includes(option.label)}
              onClick={() => toggle(option.label)}
            >
              <span className="qc-option-label">{option.label}</span>
              {option.description ? (
                <span className="qc-option-desc">{option.description}</span>
              ) : null}
            </button>
          ))}
        </div>
      ) : (
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
      )}
      <div className="qc-actions">
        <button
          type="button"
          className="primary"
          disabled={!canSubmit || answerRequest.isPending}
          onClick={submit}
        >
          {answerRequest.isPending ? 'Sending…' : 'Answer'}
        </button>
        {answerRequest.isError ? (
          <div className="qc-error">{answerRequest.error.message}</div>
        ) : null}
      </div>
    </div>
  );
}

function RequestStatus({
  request,
  onUndo,
  undoPending,
}: {
  request: AiRequest;
  onUndo(): void;
  undoPending: boolean;
}) {
  const [detailsOpen, setDetailsOpen] = useState(false);
  const inFlight =
    request.status === 'pending' || request.status === 'working' || request.status === 'answered';

  return (
    <div className={`ai-request ai-request-${request.status}`}>
      <div className="ai-request-line">
        <span className={`ai-status${inFlight ? ' ai-pulse' : ''}`}>
          {STATUS_LABEL[request.status]}
        </span>
        <span className="ai-prompt" title={request.prompt}>
          {request.prompt}
        </span>
        {request.status === 'done' && request.changes.length > 0 ? (
          <>
            <button type="button" className="ai-mini" onClick={() => setDetailsOpen(!detailsOpen)}>
              {detailsOpen ? 'Hide' : 'Show'} {request.changes.length} files
            </button>
            <button type="button" className="ai-mini" disabled={undoPending} onClick={onUndo}>
              Undo
            </button>
          </>
        ) : null}
      </div>
      {request.summary ? (
        <div className={`ai-summary${request.status === 'failed' ? ' ai-failed' : ''}`}>
          {request.summary}
        </div>
      ) : null}
      {detailsOpen ? (
        <ul className="ai-changes">
          {request.changes.map((change) => (
            <li key={change.path}>
              <code>{change.path}</code> — {change.reason}
            </li>
          ))}
        </ul>
      ) : null}
      {request.unmatched.length > 0 ? (
        <ul className="ai-unmatched">
          {request.unmatched.map((entry) => (
            <li key={entry.pattern}>
              <strong>{entry.pattern}</strong> — {entry.why}
            </li>
          ))}
        </ul>
      ) : null}
      {request.status === 'awaiting_input' && request.question ? (
        <AnswerForm request={request} />
      ) : null}
    </div>
  );
}

// Persistent input strip docked under the body. Hidden once the review is
// submitted; disabled with a hint while no Claude session is attached.
export function AiBar({ session }: { session: SessionResponse }) {
  const { slug } = useReview();
  const createRequest = useCreateAiRequest(slug);
  const undoRequest = useUndoAiRequest(slug);
  const { peerPresent } = useEventStream();
  const [prompt, setPrompt] = useState('');
  const [historyOpen, setHistoryOpen] = useState(false);
  // Tick a clock only while a request is queued, so the "still queued" hint can
  // appear once it has waited too long without mirroring any server state.
  const [now, setNow] = useState(() => Date.now());

  const latest = session.aiRequests[0];
  useEffect(() => {
    if (latest?.status !== 'pending') return;
    const id = setInterval(() => setNow(Date.now()), 15_000);
    return () => clearInterval(id);
  }, [latest?.status]);

  if (session.review.status === 'submitted') return null;

  const connected = peerPresent ?? false;
  const stalePending =
    connected &&
    latest !== undefined &&
    latest.status === 'pending' &&
    now - new Date(latest.createdAt).getTime() > STALE_PENDING_MS;

  function send() {
    const text = prompt.trim();
    if (!text) return;
    createRequest.mutate(text);
    setPrompt('');
  }

  return (
    <footer className="ai-bar">
      {latest ? (
        <RequestStatus
          key={latest.id}
          request={latest}
          onUndo={() => undoRequest.mutate(latest.id)}
          undoPending={undoRequest.isPending}
        />
      ) : null}
      <div className="ai-input-row">
        <input
          type="text"
          value={prompt}
          disabled={!connected}
          placeholder='Ask Claude — e.g. "mark every import-only rename as viewed"'
          onChange={(e) => setPrompt(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              send();
            }
          }}
        />
        <button
          type="button"
          className="primary"
          disabled={!connected || !prompt.trim() || createRequest.isPending}
          onClick={send}
        >
          Send
        </button>
        {session.aiRequests.length > 0 ? (
          <span className="ai-history">
            <button type="button" onClick={() => setHistoryOpen(!historyOpen)}>
              History ({session.aiRequests.length})
            </button>
            {historyOpen ? (
              <div className="ai-history-pop">
                {session.aiRequests.map((request) => (
                  <RequestStatus
                    key={request.id}
                    request={request}
                    onUndo={() => undoRequest.mutate(request.id)}
                    undoPending={undoRequest.isPending}
                  />
                ))}
              </div>
            ) : null}
          </span>
        ) : null}
      </div>
      {!connected ? (
        <div className="ai-hint">
          Claude is not connected — run /cc-review:start in the Claude session to enable AI actions.
        </div>
      ) : null}
      {stalePending ? (
        <div className="ai-hint">
          Still queued — Claude may not have picked this up. Resume /cc-review:start in the Claude
          session to run it; otherwise it expires shortly.
        </div>
      ) : null}
    </footer>
  );
}
