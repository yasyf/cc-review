import { useState } from 'react';
import { useSession } from '../lib/api';
import { useReview } from '../lib/review-context';
import { fileHeaderThreads } from '../lib/threads';
import { CommentThread } from './CommentThread';
import { PopoverButton } from './PopoverButton';
import { SubjectComposer } from './SubjectComposer';
import { Button } from './ui/Button';
import { Icon } from './ui/Icon';

export function FileThreads({ sectionKey, path }: { sectionKey: string; path: string }) {
  const { slug, version } = useReview();
  const { data } = useSession(slug, version);
  const [composing, setComposing] = useState(false);

  if (!data) return null;
  const section = data.sections.find((s) => s.sectionKey === sectionKey);
  if (!section) return null;
  const threads = fileHeaderThreads(data.comments, sectionKey, path);
  const canComment = data.review.kind === 'pr' && data.review.status === 'open';
  if (threads.length === 0 && !canComment) return null;

  return (
    <PopoverButton
      popoverLabel={`Comments on ${path}`}
      label={
        <>
          <Icon name="comment" size={14} />
          {threads.length > 0 ? `${threads.length} file comment${threads.length === 1 ? '' : 's'}` : 'Comment on file'}
        </>
      }
    >
      <div className="file-threads">
        {threads.map((comment) => (
          <CommentThread key={comment.id} commentId={comment.id} />
        ))}
        {canComment ? (
          composing ? (
            <SubjectComposer
              sectionId={section.sectionId}
              filePath={path}
              placeholder={`Comment on ${path}…`}
              submitLabel="Comment on file"
              onDone={() => setComposing(false)}
            />
          ) : (
            <Button size="sm" onClick={() => setComposing(true)}>
              Comment on file
            </Button>
          )
        ) : null}
      </div>
    </PopoverButton>
  );
}
