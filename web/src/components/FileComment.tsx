import { useRef, useState } from 'react';
import { useSession } from '../lib/api';
import { useReview } from '../lib/review-context';
import { SubjectComposer } from './SubjectComposer';
import { IconButton } from './ui/Button';
import { Popover } from './ui/Popover';

export function FileComment({ sectionKey, path }: { sectionKey: string; path: string }) {
  const { slug, version } = useReview();
  const { data } = useSession(slug, version);
  const anchor = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);

  const section = data?.sections.find((s) => s.sectionKey === sectionKey);
  if (!data || !section || data.review.kind !== 'pr' || data.review.status !== 'open') return null;

  return (
    <>
      <IconButton ref={anchor} icon="comment" label="Comment on file" aria-expanded={open} onClick={() => setOpen(!open)} />
      {open ? (
        <Popover anchor={anchor} label={`Comment on ${path}`} className="file-comment-popover" onClose={() => setOpen(false)}>
          <SubjectComposer
            sectionId={section.sectionId}
            filePath={path}
            placeholder={`Comment on ${path}…`}
            submitLabel="Comment on file"
            onDone={() => setOpen(false)}
          />
        </Popover>
      ) : null}
    </>
  );
}
