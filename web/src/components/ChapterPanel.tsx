import { useMemo } from 'react';
import { useSetFileStates } from '../lib/api';
import { conversationByItem } from '../lib/conversation';
import { fileItemId } from '../lib/diff';
import type { FileRef } from '../lib/diff';
import { useReview } from '../lib/review-context';
import type { Section, SessionResponse } from '../lib/types';
import { useViewPrefs } from '../lib/view-prefs';
import { FileRow } from './FileRow';
import type { RowFile } from './FileRow';
import { HiddenFilesStrip } from './HiddenFilesStrip';

// The sidebar file list for story mode: each section's chapters in narrative
// order, under a per-section head when the review spans more than one section.
// Hidden files stay out of the chapters and surface in the unhide strip below.
export function ChapterPanel({
  session,
  onSelectFile,
}: {
  session: SessionResponse;
  onSelectFile(ref: FileRef): void;
}) {
  const { slug, version } = useReview();
  const { clearExpandOverride } = useViewPrefs();
  const { mutate: mutateStates } = useSetFileStates(slug, version);

  const conversations = useMemo(() => conversationByItem(session.comments), [session.comments]);
  const showHeads = session.sections.length > 1;

  function toggleReviewed(sectionKey: string, path: string, reviewed: boolean) {
    if (!reviewed) clearExpandOverride(fileItemId(sectionKey, path));
    mutateStates([{ sectionKey, path, reviewed: !reviewed }]);
  }

  const hidden: FileRef[] = session.sections.flatMap((s) =>
    s.files
      .filter((f) => s.fileStates[f.path]?.hidden)
      .map((f) => ({ sectionKey: s.sectionKey, path: f.path })),
  );

  const renderRow = (section: Section, file: RowFile) => {
    const reviewed = section.fileStates[file.path]?.reviewed ?? false;
    const convo = conversations.get(fileItemId(section.sectionKey, file.path));
    return (
      <FileRow
        key={file.path}
        file={file}
        reviewed={reviewed}
        commentCount={convo?.openCount ?? 0}
        needsReply={convo?.needsReply ?? false}
        onSelect={() => onSelectFile({ sectionKey: section.sectionKey, path: file.path })}
        onToggle={() => toggleReviewed(section.sectionKey, file.path, reviewed)}
      />
    );
  };

  return (
    <>
      <div className="chapter-panel">
        {session.sections.map((section) => {
          const org = section.organization;
          const sectionTotal = section.files.filter((f) => !section.fileStates[f.path]?.hidden).length;
          const sectionReviewed = section.files.filter(
            (f) => !section.fileStates[f.path]?.hidden && section.fileStates[f.path]?.reviewed,
          ).length;
          return (
            <section key={section.sectionKey} className="section-group">
              {showHeads ? (
                <header className="section-group-head">
                  <span className="section-group-title">
                    {section.pending ? 'Working tree' : section.branch}
                  </span>
                  <span className="chapter-progress">
                    {sectionReviewed}/{sectionTotal}
                  </span>
                </header>
              ) : null}
              {org?.overview ? <p className="chapter-overview">{org.overview}</p> : null}
              {org ? (
                org.chapters.map((chapter, index) => {
                  const rows = chapter.files.filter((f) => !section.fileStates[f.path]?.hidden);
                  const reviewedCount = rows.filter((f) => section.fileStates[f.path]?.reviewed).length;
                  return (
                    // Titles come from the model and are not unique-validated.
                    <section key={`${chapter.title}-${index}`} className="chapter">
                      <header className="chapter-head">
                        <span className="chapter-title">{chapter.title}</span>
                        <span className="chapter-progress">
                          {reviewedCount}/{rows.length}
                        </span>
                      </header>
                      {chapter.summary ? <p className="chapter-summary">{chapter.summary}</p> : null}
                      {rows.map((file) => renderRow(section, file))}
                    </section>
                  );
                })
              ) : (
                section.files
                  .filter((f) => !section.fileStates[f.path]?.hidden)
                  .map((f) => renderRow(section, { path: f.path }))
              )}
            </section>
          );
        })}
      </div>
      <HiddenFilesStrip files={hidden} />
    </>
  );
}
