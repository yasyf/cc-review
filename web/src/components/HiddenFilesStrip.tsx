import { useSetFileStates } from '../lib/api';
import type { FileRef } from '../lib/diff';
import { useReview } from '../lib/review-context';

// The unhide affordance shared by every sidebar file panel; `files` is the
// already-filtered hidden set, aggregated across sections.
export function HiddenFilesStrip({ files }: { files: FileRef[] }) {
  const { slug, version } = useReview();
  const { mutate: mutateStates } = useSetFileStates(slug, version);

  if (files.length === 0) return null;

  return (
    <div className="hidden-files">
      <div className="hidden-files-head">Hidden files ({files.length})</div>
      {files.map((f) => (
        <div key={`${f.sectionKey}:${f.path}`} className="hidden-file">
          <span className="hidden-file-path" title={f.path}>
            {f.path}
          </span>
          <button
            type="button"
            onClick={() => mutateStates([{ sectionKey: f.sectionKey, path: f.path, hidden: false }])}
          >
            Unhide
          </button>
        </div>
      ))}
    </div>
  );
}
