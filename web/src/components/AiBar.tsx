import { useState } from 'react';
import { useCreateAiRequest, useUndoAiRequest } from '../lib/api';
import { useReview } from '../lib/review-context';
import type { AiRequest, SessionResponse } from '../lib/types';

const STATUS_LABEL: Record<AiRequest['status'], string> = {
  pending: 'queued…',
  working: 'working…',
  done: 'done',
  failed: 'failed',
  undone: 'undone',
};

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
  const inFlight = request.status === 'pending' || request.status === 'working';

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
    </div>
  );
}

// Persistent input strip docked under the body. Hidden once the review is
// submitted; disabled with a hint while no Claude session is attached.
export function AiBar({ session }: { session: SessionResponse }) {
  const { slug } = useReview();
  const createRequest = useCreateAiRequest(slug);
  const undoRequest = useUndoAiRequest(slug);
  const [prompt, setPrompt] = useState('');
  const [historyOpen, setHistoryOpen] = useState(false);

  if (session.review.status === 'submitted') return null;

  const connected = session.claudeConnected;
  const latest = session.aiRequests[0];

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
          Claude is not connected — run /review:start in the Claude session to enable AI actions.
        </div>
      ) : null}
    </footer>
  );
}
