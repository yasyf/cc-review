import { useRetryComment } from '../lib/api';
import type { Author, ReviewKind, SyncState } from '../lib/types';
import { Button } from './ui/Button';
import { Icon } from './ui/Icon';
import { Tooltip } from './ui/Tooltip';

interface Authored {
  author: Author;
  authorLogin: string;
  authorAvatarUrl: string;
}

export function authorName(entry: Authored, kind: ReviewKind): string {
  if (kind === 'pr' && entry.authorLogin) return entry.authorLogin;
  return entry.author === 'claude' ? 'Claude' : 'You';
}

export function AuthorAvatar({ entry }: { entry: Authored }) {
  if (entry.authorAvatarUrl) {
    return <img className="avatar avatar-img" src={entry.authorAvatarUrl} alt="" width={20} height={20} />;
  }
  return (
    <div className={`avatar avatar-${entry.author}`}>
      {entry.author === 'claude' ? 'C' : (entry.authorLogin || 'Y').charAt(0).toUpperCase()}
    </div>
  );
}

export function AuthorName({ entry, kind }: { entry: Authored; kind: ReviewKind }) {
  return (
    <span className="reply-who">
      {authorName(entry, kind)}
      {entry.author === 'claude' && kind === 'pr' ? <span className="claude-badge">Claude</span> : null}
    </span>
  );
}

export function SyncStatus({
  commentId,
  replyId,
  syncState,
  syncError,
}: {
  commentId: string;
  replyId?: string;
  syncState: SyncState;
  syncError: string;
}) {
  const retry = useRetryComment();
  if (syncState === 'posting') {
    return <span className="sync-posting" role="status" aria-label="Posting to GitHub" />;
  }
  if (syncState !== 'failed') return null;
  return (
    <span className="sync-failed">
      <Tooltip label={syncError}>
        <span className="sync-failed-label" tabIndex={0}>
          Not posted
        </span>
      </Tooltip>
      <Button size="sm" variant="ghost" disabled={retry.isPending} onClick={() => retry.mutate(replyId === undefined ? { commentId } : { commentId, replyId })}>
        <Icon name="refresh" size={12} />
        {retry.isPending ? 'Retrying…' : 'Retry'}
      </Button>
    </span>
  );
}
