import { useRef, useState } from 'react';
import { useSession } from '../lib/api';
import { commentSectionKey } from '../lib/diff/items';
import { useReview } from '../lib/review-context';
import { isStripThread } from '../lib/threads';
import { CommentThread } from './CommentThread';
import { SubjectComposer } from './SubjectComposer';
import { Button } from './ui/Button';
import { Icon } from './ui/Icon';
import { Popover } from './ui/Popover';
import { Tooltip } from './ui/Tooltip';

export function FileComment({ sectionKey, path }: { sectionKey: string; path: string }) {
  const { slug, version } = useReview();
  const { data } = useSession(slug, version);
  const anchor = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);

  const section = data?.sections.find((s) => s.sectionKey === sectionKey);
  if (!data || !section) return null;
  const threads = data.comments.filter(
    (c) => isStripThread(c) && c.filePath === path && commentSectionKey(c) === sectionKey,
  );
  const canComment = data.review.kind === 'pr' && data.review.status === 'open';
  if (threads.length === 0 && !canComment) return null;
  const label = threads.length > 0 ? `${threads.length} file comment${threads.length === 1 ? '' : 's'}` : 'Comment on file';

  return (
    <>
      <Tooltip label={label} describe={false}>
        <Button
          ref={anchor}
          size="sm"
          variant="ghost"
          className={threads.length > 0 ? 'file-comment-count' : 'btn-icon'}
          aria-label={label}
          aria-expanded={open}
          onClick={() => setOpen(!open)}
        >
          <Icon name="comment" size={14} />
          {threads.length > 0 ? threads.length : null}
        </Button>
      </Tooltip>
      {open ? (
        <Popover anchor={anchor} label={`Comments on ${path}`} className="file-comment-popover" onClose={() => setOpen(false)}>
          {threads.map((c) => (
            <CommentThread key={c.id} commentId={c.id} />
          ))}
          {canComment ? (
            <SubjectComposer
              sectionId={section.sectionId}
              filePath={path}
              placeholder={`Comment on ${path}…`}
              submitLabel="Comment on file"
              onDone={() => setOpen(false)}
            />
          ) : null}
        </Popover>
      ) : null}
    </>
  );
}
