import type { SessionResponse } from '../lib/types';
import { useViewPrefs } from '../lib/view-prefs';
import type { ViewMode } from '../lib/view-prefs';
import { TurnLegend } from './TurnLegend';

const MODES: { id: ViewMode; label: string }[] = [
  { id: 'default', label: 'Default' },
  { id: 'story', label: 'Story' },
  { id: 'todo', label: 'Todo' },
];

export function DiffToolbar({ session }: { session: SessionResponse }) {
  const { viewMode, setViewMode, hideReviewed, setHideReviewed } = useViewPrefs();

  const hasOrganization = session.organization !== null;
  const organizing = session.aiRequests.some(
    (r) => r.source === 'system' && (r.status === 'pending' || r.status === 'working'),
  );

  return (
    <div className="diff-toolbar">
      <div className="seg" role="tablist">
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
      <label className="hide-reviewed">
        <input
          type="checkbox"
          checked={hideReviewed}
          onChange={(e) => setHideReviewed(e.target.checked)}
        />
        Hide reviewed
      </label>
    </div>
  );
}
