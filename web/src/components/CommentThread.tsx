import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { isAutomated } from '../lib/automation';
import { useCreateReply, useResolveComment, useSession } from '../lib/api';
import { clearDraft, readDraft, replyDraftKey, writeDraft } from '../lib/drafts';
import { useReview } from '../lib/review-context';
import { useUnread } from '../lib/unread';
import { STATUS_NOTICES } from '../lib/status';
import { relativeTime } from '../lib/time';
import type { Comment, Reply, ReviewKind, ReviewStatus } from '../lib/types';
import { Markdown } from './ui/Markdown';
import { QuestionCard } from './QuestionCard';
import { AuthorAvatar, AuthorName, SyncStatus, authorName } from './ThreadAuthor';
import { Button } from './ui/Button';
import { Icon } from './ui/Icon';

function Entry({
  entry,
  kind,
  meta,
  children,
}: {
  entry: Comment | Reply;
  kind: ReviewKind;
  meta?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className={`entry entry-${entry.author}`}>
      <AuthorAvatar entry={entry} />
      <div className="entry-main">
        <div className="entry-meta">
          <AuthorName entry={entry} kind={kind} />
          <time className="entry-time" dateTime={entry.createdAt} title={new Date(entry.createdAt).toLocaleString()}>
            {relativeTime(entry.createdAt)}
          </time>
          {meta}
        </div>
        {children}
      </div>
    </div>
  );
}

function ReplyEntry({ reply, kind, status }: { reply: Reply; kind: ReviewKind; status: ReviewStatus }) {
  return (
    <Entry
      entry={reply}
      kind={kind}
      meta={
        <>
          {kind === 'local' ? <span className="entry-kind">{reply.kind}</span> : null}
          <SyncStatus commentId={reply.commentId} replyId={reply.id} syncState={reply.syncState} syncError={reply.syncError} />
        </>
      }
    >
      {reply.kind === 'ask' ? (
        <QuestionCard reply={reply} commentId={reply.commentId} status={status} />
      ) : (
        <Markdown className="entry-body" source={reply.body} />
      )}
    </Entry>
  );
}

function ReplyBox({ commentId, trailing }: { commentId: string; trailing: ReactNode }) {
  const createReply = useCreateReply();
  const [answer, setAnswer] = useState(() => readDraft(replyDraftKey(commentId)));
  const [open, setOpen] = useState(answer !== '');

  function update(text: string) {
    setAnswer(text);
    writeDraft(replyDraftKey(commentId), text);
  }

  function send() {
    const body = answer.trim();
    if (!body) return;
    createReply.mutate({ commentId, body });
    clearDraft(replyDraftKey(commentId));
    setAnswer('');
    setOpen(false);
  }

  if (!open) {
    return (
      <div className="thread-footer">
        <button type="button" className="reply-trigger" onClick={() => setOpen(true)}>
          Reply…
        </button>
        {trailing}
      </div>
    );
  }

  return (
    <div className="thread-reply">
      <textarea
        autoFocus
        value={answer}
        placeholder="Reply…"
        onChange={(e) => update(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
            e.preventDefault();
            send();
          }
          if (e.key === 'Escape' && !answer.trim()) setOpen(false);
        }}
      />
      <div className="thread-footer">
        <span className="thread-footer-hint">⌘⏎ to send</span>
        {trailing}
        <Button size="sm" variant="ghost" onClick={() => setOpen(false)}>
          Cancel
        </Button>
        <Button size="sm" variant="primary" disabled={createReply.isPending || !answer.trim()} onClick={send}>
          Reply
        </Button>
      </div>
    </div>
  );
}

export function CommentThread({ commentId, context = false }: { commentId: string; context?: boolean }) {
  const { slug, version } = useReview();
  const { data } = useSession(slug, version);
  const { markSeen } = useUnread();
  const resolveComment = useResolveComment();
  const rootRef = useRef<HTMLDivElement>(null);
  const [visible, setVisible] = useState(false);
  const [expanded, setExpanded] = useState(false);

  const comment = data?.comments.find((c) => c.id === commentId);
  const mounted = comment !== undefined;

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
  const automated = isAutomated(comment);
  const status = data.review.status;
  const kind = data.review.kind;

  if (resolved && !expanded) {
    return (
      <div ref={rootRef} className="thread thread-collapsed">
        <Icon name="check" size={12} />
        <span className="thread-collapsed-summary">
          Resolved · {authorName(comment, kind)}
          {comment.replies.length > 0 ? ` · ${comment.replies.length} repl${comment.replies.length === 1 ? 'y' : 'ies'}` : ''}
        </span>
        <Button size="sm" variant="ghost" onClick={() => setExpanded(true)}>
          Show
        </Button>
      </div>
    );
  }

  const resolveButton = (
    <Button
      size="sm"
      variant="ghost"
      className="thread-resolve"
      disabled={resolveComment.isPending}
      onClick={() => resolveComment.mutate({ id: comment.id, status: resolved ? 'open' : 'resolved' })}
    >
      {resolved ? 'Reopen' : 'Resolve'}
    </Button>
  );

  return (
    <div ref={rootRef} className={`thread${resolved ? ' thread-resolved' : ''}`}>
      {context && comment.filePath ? (
        <div className="thread-context">
          <code>{comment.filePath}</code>
          {comment.subject === 'line' ? <span className="dim">L{comment.range.end}</span> : null}
        </div>
      ) : null}
      {comment.outdated && comment.lineContent ? <code className="thread-line">{comment.lineContent}</code> : null}
      <Entry
        entry={comment}
        kind={kind}
        meta={
          <>
            {comment.outdated ? (
              <span className="entry-tag">
                Outdated · L{comment.range.start}
                {comment.range.end !== comment.range.start ? `–${comment.range.end}` : ''}
              </span>
            ) : null}
            <SyncStatus commentId={comment.id} syncState={comment.syncState} syncError={comment.syncError} />
            {comment.remoteUrl ? (
              <a className="entry-link" href={comment.remoteUrl} target="_blank" rel="noreferrer" aria-label="Open on GitHub" title="Open on GitHub">
                <Icon name="external" size={12} />
              </a>
            ) : null}
          </>
        }
      >
        <Markdown className="entry-body" source={comment.body} />
      </Entry>

      {comment.replies.map((reply) => (
        <ReplyEntry key={reply.id} reply={reply} kind={kind} status={status} />
      ))}

      {automated ? null : status !== 'open' ? (
        <div className="thread-footer thread-footer-frozen">
          {status === 'submitted' ? 'Review submitted — feedback is frozen.' : STATUS_NOTICES[status]}
        </div>
      ) : (
        <ReplyBox
          commentId={comment.id}
          trailing={
            <>
              {resolved ? (
                <Button size="sm" variant="ghost" onClick={() => setExpanded(false)}>
                  Hide
                </Button>
              ) : null}
              {resolveButton}
            </>
          }
        />
      )}
    </div>
  );
}
