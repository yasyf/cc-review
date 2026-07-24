import { commentItemId } from '../lib/diff';
import { fileOrder } from '../lib/order';
import { isUnread, useUnread } from '../lib/unread';
import type { Comment, SessionResponse } from '../lib/types';
import { useViewPrefs } from '../lib/view-prefs';

interface CommentGroup {
  filePath: string;
  branch: string;
  pending: boolean;
  comments: Comment[];
}

function excerpt(text: string, max = 120): string {
  const flat = text.replace(/\s+/g, ' ').trim();
  return flat.length > max ? `${flat.slice(0, max)}…` : flat;
}

export function CommentsPanel({
  session,
  onSelectComment,
}: {
  session: SessionResponse;
  onSelectComment(comment: Comment): void;
}) {
  const { seen } = useUnread();
  const { viewMode } = useViewPrefs();
  const showBranch = session.sections.length > 1;

  const order = fileOrder(session, viewMode);
  const groups = new Map<string, CommentGroup>();
  for (const comment of session.comments) {
    const id = commentItemId(comment);
    const group =
      groups.get(id) ??
      { filePath: comment.filePath, branch: comment.branch, pending: comment.pending, comments: [] };
    group.comments.push(comment);
    groups.set(id, group);
  }
  const ordered = [...groups.entries()].sort(
    ([a], [b]) => (order.get(a) ?? Infinity) - (order.get(b) ?? Infinity),
  );

  if (ordered.length === 0) {
    return <div className="sidebar-empty">No comments yet — select lines in the diff to start a thread.</div>;
  }

  return (
    <div className="comments-panel">
      {ordered.map(([id, group]) => (
        <div key={id} className="comment-group">
          <div className="comment-group-file">
            {group.filePath}
            {showBranch ? (
              <span className="row-branch">{group.pending ? 'pending' : group.branch}</span>
            ) : null}
          </div>
          {group.comments
            .slice()
            .sort((a, b) => a.range.end - b.range.end)
            .map((comment) => {
              const unread = isUnread(comment, seen);
              const who = comment.origin === 'claude' ? 'Claude' : 'You';
              return (
                <button
                  key={comment.id}
                  type="button"
                  className={`comment-card${unread ? ' comment-card-unread' : ''}${
                    comment.status === 'resolved' ? ' comment-card-resolved' : ''
                  }`}
                  onClick={() => onSelectComment(comment)}
                >
                  <span className="comment-card-head">
                    {unread ? <span className="unread-dot" /> : null}
                    {who} commented on Line {comment.range.end}
                    {comment.replies.length > 0 ? (
                      <span className="comment-card-count">{comment.replies.length}</span>
                    ) : null}
                  </span>
                  <span className="comment-card-body">{excerpt(comment.body)}</span>
                </button>
              );
            })}
        </div>
      ))}
    </div>
  );
}
