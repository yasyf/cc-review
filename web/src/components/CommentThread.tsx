import { useEffect, useRef, useState } from 'react';
import { useCreateReply, useResolveComment, useSession } from '../lib/api';
import { clearDraft, readDraft, replyDraftKey, writeDraft } from '../lib/drafts';
import { useReview } from '../lib/review-context';
import { useUnread } from '../lib/unread';
import type { Origin, Reply } from '../lib/types';

function Avatar({ origin }: { origin: Origin }) {
  return <div className={`avatar avatar-${origin}`}>{origin === 'claude' ? 'C' : 'Y'}</div>;
}

function ReplyBubble({ reply, onChoose }: { reply: Reply; onChoose(option: string): void }) {
  const who = reply.origin === 'claude' ? 'Claude' : 'You';
  return (
    <div className={`reply reply-${reply.origin} reply-kind-${reply.kind}`}>
      <Avatar origin={reply.origin} />
      <div className="bubble">
        <div className="reply-meta">
          <span className="reply-who">{who}</span>
          <span className="reply-kind">{reply.kind}</span>
        </div>
        <div className="reply-body">{reply.body}</div>
        {reply.kind === 'option' && reply.options && reply.options.length > 0 ? (
          <div className="reply-options">
            {reply.options.map((option) => (
              <button key={option} type="button" className="option-btn" onClick={() => onChoose(option)}>
                {option}
              </button>
            ))}
          </div>
        ) : null}
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
  }, [mounted]);

  useEffect(() => {
    if (visible && comment) markSeen(comment);
  }, [visible, comment, markSeen]);

  if (!comment) return null;

  const resolved = comment.status === 'resolved';

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

  function chooseOption(reply: Reply, option: string) {
    createReply.mutate({ commentId, answer: option, questionReplyId: reply.id });
  }

  return (
    <div ref={rootRef} className={`thread${resolved ? ' thread-resolved' : ''}`}>
      <div className="thread-head">
        <code className="thread-line">{comment.lineContent}</code>
        <button
          type="button"
          className="resolve-btn"
          disabled={resolveComment.isPending}
          onClick={() =>
            resolveComment.mutate({ id: comment.id, status: resolved ? 'open' : 'resolved' })
          }
        >
          {resolved ? 'Reopen' : 'Resolve'}
        </button>
      </div>

      <div className={`reply reply-${comment.origin}`}>
        <Avatar origin={comment.origin} />
        <div className="bubble">
          <div className="reply-meta">
            <span className="reply-who">{comment.origin === 'claude' ? 'Claude' : 'You'}</span>
          </div>
          <div className="reply-body">{comment.body}</div>
        </div>
      </div>

      {comment.replies.map((reply) => (
        <ReplyBubble key={reply.id} reply={reply} onChoose={(option) => chooseOption(reply, option)} />
      ))}

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
        <button type="button" disabled={createReply.isPending || !answer.trim()} onClick={sendAnswer}>
          Send
        </button>
      </div>
    </div>
  );
}
