import { useSetFileStates } from '../lib/api';
import type { FileRef } from '../lib/diff';
import { useReview } from '../lib/review-context';
import { Button } from './ui/Button';
import { Tooltip } from './ui/Tooltip';

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
          <Tooltip label={f.path}>
            <span className="hidden-file-path" tabIndex={0}>
              {f.path}
            </span>
          </Tooltip>
          <Button
            size="sm"
            onClick={() => mutateStates([{ sectionKey: f.sectionKey, path: f.path, hidden: false }])}
          >
            Unhide
          </Button>
        </div>
      ))}
    </div>
  );
}
