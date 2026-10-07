import { useState } from 'react';
import { useSession } from '../lib/api';
import { useReview } from '../lib/review-context';
import { CommentThread } from './CommentThread';
import { Icon } from './ui/Icon';

export function FileStrip({ commentIds }: { commentIds: readonly string[] }) {
  const { slug, version } = useReview();
  const { data } = useSession(slug, version);
  const [open, setOpen] = useState(false);
  if (!data) return null;

  const comments = data.comments.filter((c) => commentIds.includes(c.id));
  const outdated = comments.filter((c) => c.outdated).length;
  const fileLevel = comments.length - outdated;
  const parts = [
    fileLevel > 0 ? `${fileLevel} file comment${fileLevel === 1 ? '' : 's'}` : null,
    outdated > 0 ? `${outdated} outdated` : null,
  ].filter(Boolean);

  return (
    <div className="file-strip">
      <button type="button" className="file-strip-toggle" aria-expanded={open} onClick={() => setOpen(!open)}>
        <Icon name={open ? 'chevron-down' : 'chevron-right'} size={12} />
        <Icon name="comment" size={12} />
        {parts.join(' · ')}
      </button>
      {open ? (
        <div className="file-strip-threads">
          {comments.map((c) => (
            <CommentThread key={c.id} commentId={c.id} />
          ))}
        </div>
      ) : null}
    </div>
  );
}
