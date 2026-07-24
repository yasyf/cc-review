import { useSession, useSetFileStates } from '../lib/api';
import { fileItemId } from '../lib/diff';
import { chapterFileOf } from '../lib/order';
import { useReview } from '../lib/review-context';
import { useViewPrefs } from '../lib/view-prefs';

// Rendered through CodeView's renderHeaderMetadata portal; like CommentThread
// it self-subscribes to the session cache instead of receiving it via props.
export function FileHeaderControls({ sectionKey, path }: { sectionKey: string; path: string }) {
  const { slug, version } = useReview();
  const { data } = useSession(slug, version);
  const { expandOverrides, toggleExpandOverride, clearExpandOverride } = useViewPrefs();
  const setStates = useSetFileStates(slug, version);

  if (!data) return null;

  const section = data.sections.find((s) => s.sectionKey === sectionKey);
  if (!section) return null;

  const itemId = fileItemId(sectionKey, path);
  const state = section.fileStates[path] ?? { reviewed: false, hidden: false };
  const cf = chapterFileOf(section.organization, path);
  const meta = section.files.find((f) => f.path === path);
  const generated = meta?.generated;
  const vendored = meta?.vendored;
  const collapsible = state.reviewed || !!generated || !!vendored;
  const expanded = expandOverrides.has(itemId) || !collapsible;

  function setReviewed(reviewed: boolean) {
    // A fresh "Viewed" always re-collapses, even after an earlier peek.
    if (reviewed) clearExpandOverride(itemId);
    setStates.mutate([{ sectionKey, path, reviewed }]);
  }

  return (
    <span className="file-controls">
      {cf?.risk ? <span className={`risk-chip risk-${cf.risk}`}>{cf.risk}</span> : null}
      {generated ? (
        <span className="gen-chip gen-chip-generated">generated</span>
      ) : vendored ? (
        <span className="gen-chip gen-chip-vendored">vendored</span>
      ) : null}
      {cf?.focus ? (
        <span className="file-focus" title={cf.focus}>
          Focus: {cf.focus}
        </span>
      ) : null}
      {cf?.rationale ? (
        <span className="file-rationale" title={cf.rationale}>
          {cf.rationale}
        </span>
      ) : null}
      {collapsible ? (
        <button
          type="button"
          className="file-expand"
          title={expanded ? 'Collapse' : 'Expand'}
          onClick={() => toggleExpandOverride(itemId)}
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
