import { useMemo, useRef, useState } from 'react';
import { useLayout } from '../lib/layout';
import { diffStats } from '../lib/stack';
import type { SessionResponse } from '../lib/types';
import { useViewPrefs } from '../lib/view-prefs';
import type { DiffStyle, ViewMode } from '../lib/view-prefs';
import { TurnLegend } from './TurnLegend';
import { Button, IconButton } from './ui/Button';
import { Icon } from './ui/Icon';
import { Popover } from './ui/Popover';
import { Tooltip } from './ui/Tooltip';

const MODES: { id: ViewMode; label: string; hint: string }[] = [
  { id: 'default', label: 'File order', hint: 'Files in path order' },
  { id: 'story', label: 'Story', hint: "Claude's chapters, in reading order" },
  { id: 'todo', label: 'Todo', hint: 'Riskiest unreviewed files first' },
];

const DIFF_STYLES: { id: DiffStyle; label: string }[] = [
  { id: 'unified', label: 'Unified' },
  { id: 'split', label: 'Split' },
];

function ViewMenu({ hasOrganization }: { hasOrganization: boolean }) {
  const { viewMode, setViewMode, focusMode, setFocusMode } = useViewPrefs();
  const anchor = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  return (
    <>
      <Tooltip label="View options" describe={false}>
        <Button
          ref={anchor}
          size="sm"
          variant="ghost"
          className="btn-icon"
          aria-label="View options"
          aria-expanded={open}
          onClick={() => setOpen(!open)}
        >
          <Icon name="more" />
        </Button>
      </Tooltip>
      {open ? (
        <Popover anchor={anchor} label="View options" className="menu" onClose={() => setOpen(false)}>
          <div className="menu-label">Order</div>
          {MODES.map((mode) => (
            <button
              key={mode.id}
              type="button"
              className="menu-item"
              role="menuitemradio"
              aria-checked={viewMode === mode.id}
              disabled={mode.id !== 'default' && !hasOrganization}
              title={mode.hint}
              onClick={() => setViewMode(mode.id)}
            >
              {mode.label}
              {viewMode === mode.id ? <Icon name="check" size={14} className="menu-check" /> : null}
            </button>
          ))}
          <div className="menu-sep" />
          <button
            type="button"
            className="menu-item"
            role="menuitemcheckbox"
            aria-checked={focusMode}
            title="Dim lines Claude marked mechanical"
            onClick={() => setFocusMode(!focusMode)}
          >
            Focus mode
            {focusMode ? <Icon name="check" size={14} className="menu-check" /> : null}
          </button>
        </Popover>
      ) : null}
    </>
  );
}

export function DiffToolbar({ session }: { session: SessionResponse }) {
  const { hideReviewed, setHideReviewed, diffStyle, setDiffStyle } = useViewPrefs();
  const { filesOpen, toggleFiles } = useLayout();
  const files = session.sections.reduce((n, s) => n + s.files.length, 0);
  const stats = useMemo(
    () =>
      session.sections.reduce(
        (acc, s) => {
          const one = diffStats(s.patchText);
          return { additions: acc.additions + one.additions, deletions: acc.deletions + one.deletions };
        },
        { additions: 0, deletions: 0 },
      ),
    [session.sections],
  );
  const hasOrganization = session.sections.some((s) => s.organization !== null);

  return (
    <div className="diff-toolbar">
      <IconButton
        icon="sidebar"
        label={filesOpen ? 'Hide file tree' : 'Show file tree'}
        shortcut="]"
        aria-pressed={filesOpen}
        onClick={toggleFiles}
      />
      <span className="diff-toolbar-stats">
        {files} file{files === 1 ? '' : 's'}
        <span className="stat-add">+{stats.additions}</span>
        <span className="stat-del">−{stats.deletions}</span>
      </span>
      <TurnLegend turns={session.turns} />
      <span className="toolbar-spacer" />
      <button
        type="button"
        className="toggle"
        role="switch"
        aria-checked={hideReviewed}
        onClick={() => setHideReviewed(!hideReviewed)}
      >
        <span className="toggle-track" aria-hidden="true" />
        Hide viewed
      </button>
      <span className="seg" role="group" aria-label="Diff layout">
        {DIFF_STYLES.map((style) => (
          <button
            key={style.id}
            type="button"
            className="seg-btn"
            aria-pressed={diffStyle === style.id}
            onClick={() => setDiffStyle(style.id)}
          >
            {style.label}
          </button>
        ))}
      </span>
      <ViewMenu hasOrganization={hasOrganization} />
    </div>
  );
}
