import { useRef, useState } from 'react';
import { useClose, useSubmit } from '../lib/api';
import { useEventStream } from '../lib/events';
import { useLayout } from '../lib/layout';
import { useReview } from '../lib/review-context';
import { STATUS_NOTICES } from '../lib/status';
import { setThemeMode, useThemeMode } from '../lib/theme';
import type { ThemeMode } from '../lib/theme';
import type { SessionResponse } from '../lib/types';
import { SubmitDialog } from './SubmitDialog';
import { Button, IconButton } from './ui/Button';
import { Icon } from './ui/Icon';
import type { IconName } from './ui/icons';
import { Popover } from './ui/Popover';
import { Tooltip } from './ui/Tooltip';

const THEMES: { mode: ThemeMode; icon: IconName; label: string }[] = [
  { mode: 'system', icon: 'monitor', label: 'System' },
  { mode: 'light', icon: 'sun', label: 'Light' },
  { mode: 'dark', icon: 'moon', label: 'Dark' },
];

function Mark() {
  return (
    <svg className="brand-mark" width={18} height={18} viewBox="0 0 18 18" aria-hidden="true">
      <rect x="1" y="1" width="16" height="16" rx="4" fill="var(--accent)" />
      <path d="M6 6.5h6M6 9h4M6 11.5h6" stroke="var(--accent-fg)" strokeWidth="1.5" strokeLinecap="round" />
    </svg>
  );
}

function ProgressRing({ done, total }: { done: number; total: number }) {
  const r = 6;
  const circumference = 2 * Math.PI * r;
  const fraction = total > 0 ? done / total : 0;
  return (
    <span className="progress-ring" aria-label={`Reviewed ${done} of ${total} files`}>
      <svg width={16} height={16} viewBox="0 0 16 16" aria-hidden="true">
        <circle cx="8" cy="8" r={r} className="progress-ring-track" />
        <circle
          cx="8"
          cy="8"
          r={r}
          className="progress-ring-fill"
          strokeDasharray={circumference}
          strokeDashoffset={circumference * (1 - fraction)}
        />
      </svg>
      <span className="progress-ring-label">
        reviewed {done}/{total}
      </span>
    </span>
  );
}

function OverflowMenu({ canClose, onClose }: { canClose: boolean; onClose(): void }) {
  const anchor = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const theme = useThemeMode();
  return (
    <>
      <Tooltip label="More" describe={false}>
        <Button
          ref={anchor}
          variant="ghost"
          size="sm"
          className="btn-icon"
          aria-label="More"
          aria-expanded={open}
          onClick={() => setOpen(!open)}
        >
          <Icon name="more" />
        </Button>
      </Tooltip>
      {open ? (
        <Popover anchor={anchor} label="Review menu" placement="bottom" className="menu" onClose={() => setOpen(false)}>
          <div className="menu-label">Theme</div>
          {THEMES.map(({ mode, icon, label }) => (
            <button
              key={mode}
              type="button"
              className="menu-item"
              role="menuitemradio"
              aria-checked={theme === mode}
              onClick={() => setThemeMode(mode)}
            >
              <Icon name={icon} size={14} />
              {label}
              {theme === mode ? <Icon name="check" size={14} className="menu-check" /> : null}
            </button>
          ))}
          {canClose ? (
            <>
              <div className="menu-sep" />
              <button
                type="button"
                className="menu-item menu-item-danger"
                onClick={() => {
                  setOpen(false);
                  onClose();
                }}
              >
                <Icon name="x" size={14} />
                Close without submitting
              </button>
            </>
          ) : null}
        </Popover>
      ) : null}
    </>
  );
}

export function TopBar({ session, hasRail }: { session: SessionResponse; hasRail: boolean }) {
  const { slug } = useReview();
  const submit = useSubmit(slug);
  const close = useClose(slug);
  const { connected } = useEventStream();
  const { railMode, toggleRail } = useLayout();
  const [submitOpen, setSubmitOpen] = useState(false);

  const { review } = session;
  const status = review.status;
  const frozenPath = session.feedbackPath ?? submit.data?.feedbackPath ?? null;
  const total = session.sections.reduce((n, s) => n + s.files.length, 0);
  const reviewed = session.sections.reduce(
    (n, s) => n + s.files.filter((f) => s.fileStates[f.path]?.reviewed).length,
    0,
  );
  const repo = review.kind === 'pr' ? review.repo : (review.repoRoot.split('/').pop() ?? review.repoRoot);

  return (
    <header className="top-bar">
      <div className="top-bar-start">
        {hasRail ? (
          <IconButton
            icon="sidebar"
            label={railMode === 'hidden' ? 'Show stack' : 'Hide stack'}
            shortcut="["
            aria-pressed={railMode !== 'hidden'}
            onClick={toggleRail}
          />
        ) : null}
        <Mark />
        <span className="top-bar-repo">{repo}</span>
        <span className="top-bar-slash">/</span>
        <span className="top-bar-branch" title={review.branch}>
          {review.branch}
        </span>
        {connected ? null : (
          <Tooltip label="Reconnecting to cc-review…">
            <span className="conn-dot" role="status" aria-label="Disconnected" tabIndex={0} />
          </Tooltip>
        )}
      </div>
      <div className="top-bar-end">
        <ProgressRing done={reviewed} total={total} />
        {status === 'open' ? null : (
          <span className="frozen">
            {status === 'submitted'
              ? frozenPath
                ? `Submitted → ${frozenPath}`
                : 'Submitted'
              : STATUS_NOTICES[status]}
          </span>
        )}
        <OverflowMenu canClose={status === 'open' || status === 'expired'} onClose={() => close.mutate()} />
        {status === 'open' ? (
          <>
            <Button variant="primary" size="sm" onClick={() => setSubmitOpen(true)}>
              {review.kind === 'pr' ? 'Submit review' : 'Send to Claude'}
            </Button>
            <SubmitDialog session={session} submit={submit} open={submitOpen} onClose={() => setSubmitOpen(false)} />
          </>
        ) : null}
      </div>
    </header>
  );
}
