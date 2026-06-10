import { isUnread, useUnread } from '../lib/unread';
import type { Comment, FileMeta } from '../lib/types';

function excerpt(text: string, max = 120): string {
  const flat = text.replace(/\s+/g, ' ').trim();
  return flat.length > max ? `${flat.slice(0, max)}…` : flat;
}

export function CommentsPanel({
  comments,
  files,
  onSelectComment,
}: {
  comments: Comment[];
  files: FileMeta[];
  onSelectComment(comment: Comment): void;
}) {
  const { seen } = useUnread();

  const fileOrder = new Map(files.map((f, i) => [f.path, i]));
  const groups = new Map<string, Comment[]>();
  for (const comment of comments) {
    const list = groups.get(comment.filePath) ?? [];
    list.push(comment);
    groups.set(comment.filePath, list);
  }
  const ordered = [...groups.entries()].sort(
    ([a], [b]) => (fileOrder.get(a) ?? Infinity) - (fileOrder.get(b) ?? Infinity),
  );

  if (ordered.length === 0) {
    return <div className="sidebar-empty">No comments yet — select lines in the diff to start a thread.</div>;
  }

  return (
    <div className="comments-panel">
      {ordered.map(([filePath, group]) => (
        <div key={filePath} className="comment-group">
          <div className="comment-group-file">{filePath}</div>
          {group
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
