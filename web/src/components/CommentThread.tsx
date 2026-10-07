import { useEffect, useRef, useState } from 'react';
import { useCreateReply, useResolveComment, useSession } from '../lib/api';
import { clearDraft, readDraft, replyDraftKey, writeDraft } from '../lib/drafts';
import { useReview } from '../lib/review-context';
import { useUnread } from '../lib/unread';
import { STATUS_NOTICES } from '../lib/status';
import type { Reply, ReviewKind, ReviewStatus } from '../lib/types';
import { Markdown } from './ui/Markdown';
import { QuestionCard } from './QuestionCard';
import { AuthorAvatar, AuthorName, SyncStatus, authorName } from './ThreadAuthor';
import { Button } from './ui/Button';
import { Icon } from './ui/Icon';

function ReplyBubble({
  reply,
  status,
  kind,
}: {
  reply: Reply;
  status: ReviewStatus;
  kind: ReviewKind;
}) {
  return (
    <div className={`reply reply-${reply.author} reply-kind-${reply.kind}`}>
      <AuthorAvatar entry={reply} />
      <div className="bubble">
        <div className="reply-meta">
          <AuthorName entry={reply} kind={kind} />
          {kind === 'local' ? <span className="reply-kind">{reply.kind}</span> : null}
          <SyncStatus
            commentId={reply.commentId}
            replyId={reply.id}
            syncState={reply.syncState}
            syncError={reply.syncError}
          />
        </div>
        {reply.kind === 'ask' ? (
          <QuestionCard reply={reply} commentId={reply.commentId} status={status} />
        ) : (
          <Markdown className="reply-body" source={reply.body} />
        )}
      </div>
    </div>
  );
}

export function CommentThread({ commentId }: { commentId: string }) {
  const { slug, version } = useReview();
  const { data } = useSession(slug, version);
  const { markSeen } = useUnread();
  const createReply = useCreateReply();
  const resolveComment = useResolveComment();
  // Rehydrate across portal remounts (virtualizer releases the file's item
  // when it scrolls far off screen, unmounting every annotation under it).
  const [answer, setAnswer] = useState(() => readDraft(replyDraftKey(commentId)));
  const rootRef = useRef<HTMLDivElement>(null);
  const [visible, setVisible] = useState(false);
  const [expanded, setExpanded] = useState(false);

  const comment = data?.comments.find((c) => c.id === commentId);
  const mounted = comment !== undefined;

  // Annotation mount is file-granular, so "rendered" is not "viewed": gate
  // markSeen on actual viewport intersection of the thread itself.
  useEffect(() => {
    const el = rootRef.current;
    if (!el) return;
    const io = new IntersectionObserver((entries) =>
      setVisible(entries.some((entry) => entry.isIntersecting)),
    );
    io.observe(el);
    return () => io.disconnect();
  }, [mounted, expanded]);

  useEffect(() => {
    if (visible && comment) markSeen(comment);
  }, [visible, comment, markSeen]);

  if (!data || !comment) return null;

  const resolved = comment.status === 'resolved';
  const status = data.review.status;
  const kind = data.review.kind;

  function updateAnswer(text: string) {
    setAnswer(text);
    writeDraft(replyDraftKey(commentId), text);
  }

  function sendAnswer() {
    const body = answer.trim();
    if (!body) return;
    createReply.mutate({ commentId, body });
    clearDraft(replyDraftKey(commentId));
    setAnswer('');
  }

  if (resolved && !expanded) {
    return (
      <div ref={rootRef} className="thread thread-resolved thread-collapsed">
        <span className="thread-collapsed-summary">
          Resolved · {authorName(comment, kind)}
          {comment.replies.length > 0 ? ` · ${comment.replies.length} replies` : ''}
        </span>
        <Button size="sm" variant="ghost" onClick={() => setExpanded(true)}>
          Show
        </Button>
      </div>
    );
  }

  return (
    <div ref={rootRef} className={`thread${resolved ? ' thread-resolved' : ''}`}>
      <div className="thread-head">
        {comment.outdated ? (
          <span className="thread-outdated">
            Outdated · was L{comment.range.start}
            {comment.range.end !== comment.range.start ? `–${comment.range.end}` : ''}
          </span>
        ) : comment.lineContent ? (
          <code className="thread-line">{comment.lineContent}</code>
        ) : null}
        <span className="thread-actions">
          {comment.remoteUrl ? (
            <a className="btn btn-ghost btn-sm" href={comment.remoteUrl} target="_blank" rel="noreferrer">
              <Icon name="external" size={12} />
              GitHub
            </a>
          ) : null}
          {resolved ? (
            <Button size="sm" variant="ghost" onClick={() => setExpanded(false)}>
              Hide
            </Button>
          ) : null}
          <Button
            size="sm"
            disabled={resolveComment.isPending}
            onClick={() =>
              resolveComment.mutate({ id: comment.id, status: resolved ? 'open' : 'resolved' })
            }
          >
            {resolved ? 'Reopen' : 'Resolve'}
          </Button>
        </span>
      </div>

      <div className={`reply reply-${comment.author}`}>
        <AuthorAvatar entry={comment} />
        <div className="bubble">
          <div className="reply-meta">
            <AuthorName entry={comment} kind={kind} />
            <SyncStatus commentId={comment.id} syncState={comment.syncState} syncError={comment.syncError} />
          </div>
          <Markdown className="reply-body" source={comment.body} />
        </div>
      </div>

      {comment.replies.map((reply) => (
        <ReplyBubble key={reply.id} reply={reply} status={status} kind={kind} />
      ))}

      {status !== 'open' ? (
        <div className="qc-hint">
          {status === 'submitted' ? 'Review submitted — feedback is frozen.' : STATUS_NOTICES[status]}
        </div>
      ) : (
        <div className="answer-box">
          <textarea
            value={answer}
            placeholder="Reply…"
            onChange={(e) => updateAnswer(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                e.preventDefault();
                sendAnswer();
              }
            }}
          />
          <Button variant="primary" disabled={createReply.isPending || !answer.trim()} onClick={sendAnswer}>
            Send
          </Button>
        </div>
      )}
    </div>
  );
}
