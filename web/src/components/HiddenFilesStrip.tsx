import { useSetFileStates } from '../lib/api';
import { useReview } from '../lib/review-context';
import type { FileMeta } from '../lib/types';

// The unhide affordance shared by every sidebar file panel; `files` is the
// already-filtered hidden set.
export function HiddenFilesStrip({ files }: { files: FileMeta[] }) {
  const { slug, version } = useReview();
  const { mutate: mutateStates } = useSetFileStates(slug, version);

  if (files.length === 0) return null;

  return (
    <div className="hidden-files">
      <div className="hidden-files-head">Hidden files ({files.length})</div>
      {files.map((f) => (
        <div key={f.path} className="hidden-file">
          <span className="hidden-file-path" title={f.path}>
            {f.path}
          </span>
          <button type="button" onClick={() => mutateStates([{ path: f.path, hidden: false }])}>
            Unhide
          </button>
        </div>
      ))}
    </div>
  );
}
