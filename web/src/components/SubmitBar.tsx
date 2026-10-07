import { ConnectionFrame } from '@cc-interact/react';
import { useState } from 'react';
import { useClose, useSubmit } from '../lib/api';
import { useEventStream } from '../lib/events';
import { useReview } from '../lib/review-context';
import { STATUS_NOTICES } from '../lib/status';
import type { SessionResponse } from '../lib/types';
import { useSidebarLayout } from '../lib/sidebar-layout';
import { Button, IconButton } from './ui/Button';
import { SubmitDialog } from './SubmitDialog';
import { ThemeToggle } from './ui/ThemeToggle';

export function SubmitBar({ session }: { session: SessionResponse }) {
  const { slug } = useReview();
  const submit = useSubmit(slug);
  const close = useClose(slug);
  const { connected } = useEventStream();
  const { mode, toggle } = useSidebarLayout();
  const [submitOpen, setSubmitOpen] = useState(false);

  const status = session.review.status;
  // Claude-authored comments are informational annotations, not reviewer TODOs.
  const openCount = session.comments.filter(
    (c) => c.status === 'open' && c.origin !== 'claude',
  ).length;
  const frozenPath = session.feedbackPath ?? submit.data?.feedbackPath ?? null;
  const total = session.sections.reduce((n, s) => n + s.files.length, 0);
  const reviewedCount = session.sections.reduce(
    (n, s) => n + s.files.filter((f) => s.fileStates[f.path]?.reviewed).length,
    0,
  );
  const branchCount = session.sections.filter((s) => !s.pending).length;

  return (
    <header className="submit-bar">
      <div className="meta">
        <IconButton
          icon="sidebar"
          label={mode === 'docked' || mode === 'overlay' ? 'Hide sidebar' : 'Show sidebar'}
          shortcut="["
          aria-pressed={mode !== 'hidden'}
          onClick={toggle}
        />
        <strong className="brand">cc-review</strong>
        <span className="branch">{session.review.branch}</span>
        <span className="dim">v{session.version}</span>
        {branchCount > 1 ? <span className="dim">{branchCount} branches</span> : null}
        <span className="dim">{total} files</span>
        <span className="dim">{openCount} open</span>
        <span className="dim">
          {reviewedCount}/{total} reviewed
        </span>
        <span className="progress-track">
          <span
            className="progress-fill"
            style={{ width: `${total > 0 ? (reviewedCount / total) * 100 : 0}%` }}
          />
        </span>
        <span className={`status status-${status}`}>{status}</span>
        <ConnectionFrame connected={connected} />
      </div>
      <div className="actions">
        <ThemeToggle />
        {status === 'open' ? (
          <>
            <Button variant="ghost" disabled={close.isPending} onClick={() => close.mutate()}>
              {close.isPending ? 'Closing…' : 'Close without submitting'}
            </Button>
            <Button variant="primary" onClick={() => setSubmitOpen(true)}>
              {session.review.kind === 'pr' ? 'Submit review' : 'Send to Claude'}
            </Button>
            <SubmitDialog session={session} submit={submit} open={submitOpen} onClose={() => setSubmitOpen(false)} />
          </>
        ) : (
          <>
            <span className="frozen">
              {status === 'submitted'
                ? frozenPath
                  ? `Submitted → ${frozenPath}`
                  : 'Submitted'
                : STATUS_NOTICES[status]}
            </span>
            {status === 'expired' && (
              <Button disabled={close.isPending} onClick={() => close.mutate()}>
                {close.isPending ? 'Closing…' : 'Close'}
              </Button>
            )}
          </>
        )}
      </div>
    </header>
  );
}
