import { useSession, useSetFileStates } from '../lib/api';
import { riskOf } from '../lib/order';
import { useReview } from '../lib/review-context';
import { useViewPrefs } from '../lib/view-prefs';

// Rendered through CodeView's renderHeaderMetadata portal; like CommentThread
// it self-subscribes to the session cache instead of receiving it via props.
export function FileHeaderControls({ path }: { path: string }) {
  const { slug, version } = useReview();
  const { data } = useSession(slug, version);
  const { expandOverrides, toggleExpandOverride, clearExpandOverride } = useViewPrefs();
  const setStates = useSetFileStates(slug, version);

  if (!data) return null;

  const state = data.fileStates[path] ?? { reviewed: false, hidden: false };
  const risk = riskOf(data.organization, path);
  const expanded = !state.reviewed || expandOverrides.has(path);

  function setReviewed(reviewed: boolean) {
    // A fresh "Viewed" always re-collapses, even after an earlier peek.
    if (reviewed) clearExpandOverride(path);
    setStates.mutate([{ path, reviewed }]);
  }

  return (
    <span className="file-controls">
      {risk ? <span className={`risk-chip risk-${risk}`}>{risk}</span> : null}
      {state.reviewed ? (
        <button
          type="button"
          className="file-expand"
          title={expanded ? 'Collapse' : 'Expand'}
          onClick={() => toggleExpandOverride(path)}
        >
          {expanded ? '▾' : '▸'}
        </button>
      ) : null}
      <label className="viewed-toggle">
        <input
          type="checkbox"
          checked={state.reviewed}
          onChange={(e) => setReviewed(e.target.checked)}
        />
        Viewed
      </label>
    </span>
  );
}
