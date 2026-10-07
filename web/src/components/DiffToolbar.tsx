import { isOrganizing } from '../lib/ai-requests';
import type { SessionResponse } from '../lib/types';
import { useViewPrefs } from '../lib/view-prefs';
import type { DiffStyle, ViewMode } from '../lib/view-prefs';
import { TurnLegend } from './TurnLegend';
import { Icon } from './ui/Icon';
import type { IconName } from './ui/icons';
import { Tooltip } from './ui/Tooltip';

const MODES: { id: ViewMode; label: string }[] = [
  { id: 'default', label: 'Default' },
  { id: 'story', label: 'Story' },
  { id: 'todo', label: 'Todo' },
];

const DIFF_STYLES: { id: DiffStyle; label: string; icon: IconName }[] = [
  { id: 'unified', label: 'Unified diff', icon: 'unified' },
  { id: 'split', label: 'Split diff', icon: 'split' },
];

export function DiffToolbar({ session }: { session: SessionResponse }) {
  const { viewMode, setViewMode, hideReviewed, setHideReviewed, focusMode, setFocusMode, diffStyle, setDiffStyle } =
    useViewPrefs();

  const hasOrganization = session.sections.some((s) => s.organization !== null);
  const organizing = isOrganizing(session.aiRequests);

  return (
    <div className="diff-toolbar">
      <div className="seg" role="tablist" aria-label="File order">
        {MODES.map((mode) => (
          <button
            key={mode.id}
            type="button"
            role="tab"
            className="seg-btn"
            aria-selected={viewMode === mode.id}
            disabled={mode.id !== 'default' && !hasOrganization}
            onClick={() => setViewMode(mode.id)}
          >
            {mode.label}
          </button>
        ))}
      </div>
      {organizing ? <span className="organizing-chip">organizing…</span> : null}
      <TurnLegend turns={session.turns} />
      <span className="toolbar-spacer" />
      <label className="hide-reviewed">
        <input type="checkbox" checked={focusMode} onChange={(e) => setFocusMode(e.target.checked)} />
        Focus mode
      </label>
      <label className="hide-reviewed">
        <input type="checkbox" checked={hideReviewed} onChange={(e) => setHideReviewed(e.target.checked)} />
        Hide reviewed
      </label>
      <span className="seg" role="group" aria-label="Diff layout">
        {DIFF_STYLES.map((style) => (
          <Tooltip key={style.id} label={style.label} describe={false}>
            <button
              type="button"
              className="seg-btn seg-icon"
              aria-pressed={diffStyle === style.id}
              aria-label={style.label}
              onClick={() => setDiffStyle(style.id)}
            >
              <Icon name={style.icon} size={14} />
            </button>
          </Tooltip>
        ))}
      </span>
    </div>
  );
}
