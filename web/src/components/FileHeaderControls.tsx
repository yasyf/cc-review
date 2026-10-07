import { useSession, useSetFileStates } from '../lib/api';
import { fileItemId } from '../lib/diff/items';
import { chapterFileOf } from '../lib/order';
import { useReview } from '../lib/review-context';
import { useViewPrefs } from '../lib/view-prefs';
import { FileThreads } from './FileThreads';
import { IconButton } from './ui/Button';
import { Tooltip } from './ui/Tooltip';

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
      {data.sections.length > 1 ? (
        <span className="section-chip">{section.prNumber ? `#${section.prNumber}` : section.branch || 'working tree'}</span>
      ) : null}
      {cf?.risk ? <span className={`risk-chip risk-${cf.risk}`}>{cf.risk}</span> : null}
      {generated ? (
        <span className="gen-chip gen-chip-generated">generated</span>
      ) : vendored ? (
        <span className="gen-chip gen-chip-vendored">vendored</span>
      ) : null}
      {cf?.focus ? (
        <Tooltip label={cf.focus}>
          <span className="file-focus" tabIndex={0}>
            Focus: {cf.focus}
          </span>
        </Tooltip>
      ) : null}
      {cf?.rationale ? (
        <Tooltip label={cf.rationale}>
          <span className="file-rationale" tabIndex={0}>
            {cf.rationale}
          </span>
        </Tooltip>
      ) : null}
      {collapsible ? (
        <IconButton
          icon={expanded ? 'chevron-down' : 'chevron-right'}
          label={expanded ? 'Collapse' : 'Expand'}
          shortcut="c"
          onClick={() => toggleExpandOverride(itemId)}
        />
      ) : null}
      <FileThreads sectionKey={sectionKey} path={path} />
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
