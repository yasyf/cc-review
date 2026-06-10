import { useMemo } from 'react';
import { useSetFileStates } from '../lib/api';
import { RISK_LEVELS, riskOf } from '../lib/order';
import { useReview } from '../lib/review-context';
import type { ChapterFile, Organization, Risk, SessionResponse } from '../lib/types';
import { useViewPrefs } from '../lib/view-prefs';
import { HiddenFilesStrip } from './HiddenFilesStrip';

interface PanelGroup {
  title: string;
  summary?: string;
  files: ChapterFile[];
}

function FileRow({
  file,
  reviewed,
  commentCount,
  onSelect,
  onToggle,
}: {
  file: ChapterFile;
  reviewed: boolean;
  commentCount: number;
  onSelect(): void;
  onToggle(): void;
}) {
  const name = file.path.split('/').pop();
  return (
    <div className={`chapter-row${reviewed ? ' chapter-row-reviewed' : ''}`} title={file.rationale}>
      <input type="checkbox" checked={reviewed} onChange={onToggle} aria-label="Viewed" />
      <button type="button" className="chapter-row-path" onClick={onSelect}>
        {name}
      </button>
      <span className={`risk-chip risk-${file.risk}`}>{file.risk}</span>
      {commentCount > 0 ? <span className="comment-card-count">{commentCount}</span> : null}
    </div>
  );
}

// The sidebar todo list for story/risk modes: story renders the chapters in
// narrative order, risk renders severity groups. Hidden files stay out of the
// groups and surface in the unhide strip below.
export function ChapterPanel({
  session,
  organization,
  onSelectFile,
}: {
  session: SessionResponse;
  organization: Organization;
  onSelectFile(path: string): void;
}) {
  const { slug, version } = useReview();
  const { viewMode, clearExpandOverride } = useViewPrefs();
  const { mutate: mutateStates } = useSetFileStates(slug, version);

  const openComments = useMemo(() => {
    const counts = new Map<string, number>();
    for (const comment of session.comments) {
      if (comment.status !== 'open') continue;
      counts.set(comment.filePath, (counts.get(comment.filePath) ?? 0) + 1);
    }
    return counts;
  }, [session.comments]);

  const groups = useMemo<PanelGroup[]>(() => {
    if (viewMode === 'risk') {
      const byRisk = new Map<Risk, ChapterFile[]>();
      // Patch order within each severity tier.
      for (const meta of session.files) {
        const risk = riskOf(organization, meta.path);
        if (!risk) continue;
        const file = organization.chapters
          .flatMap((c) => c.files)
          .find((f) => f.path === meta.path);
        if (!file) continue;
        byRisk.set(risk, [...(byRisk.get(risk) ?? []), file]);
      }
      return RISK_LEVELS.flatMap((risk) => {
        const files = byRisk.get(risk);
        return files ? [{ title: risk, files }] : [];
      });
    }
    return organization.chapters.map((chapter) => ({
      title: chapter.title,
      summary: chapter.summary,
      files: chapter.files,
    }));
  }, [viewMode, organization, session.files]);

  function toggleReviewed(file: ChapterFile, reviewed: boolean) {
    if (!reviewed) clearExpandOverride(file.path);
    mutateStates([{ path: file.path, reviewed: !reviewed }]);
  }

  const hidden = session.files.filter((f) => session.fileStates[f.path]?.hidden);

  return (
    <>
      <div className="chapter-panel">
        {viewMode === 'story' && organization.overview ? (
          <p className="chapter-overview">{organization.overview}</p>
        ) : null}
        {groups.map((group, index) => {
          const rows = group.files.filter((f) => !session.fileStates[f.path]?.hidden);
          const reviewedCount = rows.filter((f) => session.fileStates[f.path]?.reviewed).length;
          return (
            // Titles come from the model and are not unique-validated.
            <section key={`${group.title}-${index}`} className="chapter">
              <header className="chapter-head">
                <span className="chapter-title">{group.title}</span>
                <span className="chapter-progress">
                  {reviewedCount}/{rows.length}
                </span>
              </header>
              {group.summary ? <p className="chapter-summary">{group.summary}</p> : null}
              {rows.map((file) => {
                const reviewed = session.fileStates[file.path]?.reviewed ?? false;
                return (
                  <FileRow
                    key={file.path}
                    file={file}
                    reviewed={reviewed}
                    commentCount={openComments.get(file.path) ?? 0}
                    onSelect={() => onSelectFile(file.path)}
                    onToggle={() => toggleReviewed(file, reviewed)}
                  />
                );
              })}
            </section>
          );
        })}
      </div>
      <HiddenFilesStrip files={hidden} />
    </>
  );
}
