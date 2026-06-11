import { useMemo } from 'react';
import { useSetFileStates } from '../lib/api';
import { conversationByFile } from '../lib/conversation';
import { useReview } from '../lib/review-context';
import type { ChapterFile, Organization, SessionResponse } from '../lib/types';
import { useViewPrefs } from '../lib/view-prefs';
import { FileRow } from './FileRow';
import { HiddenFilesStrip } from './HiddenFilesStrip';

// The sidebar file list for story mode: chapters in narrative order. Hidden
// files stay out of the chapters and surface in the unhide strip below.
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
  const { clearExpandOverride } = useViewPrefs();
  const { mutate: mutateStates } = useSetFileStates(slug, version);

  const conversations = useMemo(() => conversationByFile(session.comments), [session.comments]);

  function toggleReviewed(file: ChapterFile, reviewed: boolean) {
    if (!reviewed) clearExpandOverride(file.path);
    mutateStates([{ path: file.path, reviewed: !reviewed }]);
  }

  const hidden = session.files.filter((f) => session.fileStates[f.path]?.hidden);

  return (
    <>
      <div className="chapter-panel">
        {organization.overview ? (
          <p className="chapter-overview">{organization.overview}</p>
        ) : null}
        {organization.chapters.map((chapter, index) => {
          const rows = chapter.files.filter((f) => !session.fileStates[f.path]?.hidden);
          const reviewedCount = rows.filter((f) => session.fileStates[f.path]?.reviewed).length;
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
              {rows.map((file) => {
                const reviewed = session.fileStates[file.path]?.reviewed ?? false;
                return (
                  <FileRow
                    key={file.path}
                    file={file}
                    reviewed={reviewed}
                    commentCount={conversations.get(file.path)?.openCount ?? 0}
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
