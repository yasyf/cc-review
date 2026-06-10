import { useState } from 'react';
import { useCreateReply, useResolveComment, useSession } from '../lib/api';
import { useReview } from '../lib/review-context';
import type { Reply } from '../lib/types';

function ReplyBubble({ reply, onChoose }: { reply: Reply; onChoose(option: string): void }) {
  const who = reply.origin === 'claude' ? 'Claude' : 'You';
  return (
    <div className={`reply reply-${reply.origin} reply-kind-${reply.kind}`}>
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
  );
}

export function CommentThread({ commentId }: { commentId: string }) {
  const { slug, version } = useReview();
  const { data } = useSession(slug, version);
  const createReply = useCreateReply();
  const resolveComment = useResolveComment();
  const [answer, setAnswer] = useState('');

  const comment = data?.comments.find((c) => c.id === commentId);
  if (!comment) return null;

  const resolved = comment.status === 'resolved';

  function sendAnswer() {
    const body = answer.trim();
    if (!body) return;
    createReply.mutate({ commentId, body });
    setAnswer('');
  }

  function chooseOption(reply: Reply, option: string) {
    createReply.mutate({ commentId, answer: option, questionReplyId: reply.id });
  }

  return (
    <div className={`thread${resolved ? ' thread-resolved' : ''}`}>
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
        <div className="reply-meta">
          <span className="reply-who">{comment.origin === 'claude' ? 'Claude' : 'You'}</span>
        </div>
        <div className="reply-body">{comment.body}</div>
      </div>

      {comment.replies.map((reply) => (
        <ReplyBubble key={reply.id} reply={reply} onChoose={(option) => chooseOption(reply, option)} />
      ))}

      <div className="answer-box">
        <textarea
          value={answer}
          placeholder="Reply…"
          onChange={(e) => setAnswer(e.target.value)}
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
