// Domain model + the realtime event union. These mirror the daemon's REST and
// SSE contract; the compiler enforces exhaustive handling of every event kind.

export type Side = 'additions' | 'deletions';

export interface LineRange {
  start: number;
  end: number;
  startSide?: Side;
  endSide?: Side;
}

export type ReviewStatus = 'open' | 'submitted';
export type CommentStatus = 'open' | 'resolved';
export type Origin = 'user' | 'claude';

// Claude streams these reply kinds under a comment; a user answer is kind 'answer'.
export type ReplyKind = 'question' | 'option' | 'clarification' | 'note' | 'answer';

export interface Review {
  id: string;
  status: ReviewStatus;
  repoRoot: string;
  branch: string;
  createdAt: string;
}

export interface Reply {
  id: string;
  commentId: string;
  origin: Origin;
  kind: ReplyKind;
  body: string;
  // Present for kind 'option' — the selectable choices Claude offered.
  options?: string[];
  // For an 'answer' reply, the question/option reply it responds to.
  questionReplyId?: string;
  createdAt: string;
}

export interface Comment {
  id: string;
  versionId: string;
  filePath: string;
  side: Side;
  range: LineRange;
  lineContent: string;
  body: string;
  origin: Origin;
  status: CommentStatus;
  createdAt: string;
  replies: Reply[];
}

export interface FileMeta {
  path: string;
  status: 'change' | 'rename-pure' | 'rename-changed' | 'new' | 'deleted';
  additions: number;
  deletions: number;
}

export interface SessionResponse {
  review: Review;
  version: number;
  // Stable id of this version row — referenced when creating a comment.
  versionId: string;
  files: FileMeta[];
  patchText: string;
  comments: Comment[];
}

export interface VersionSummary {
  versionId: string;
  version: number;
  branch: string;
  baseRef: string;
  createdAt: string;
}

// The SSE payload is a tagged union on `type`. Every frame carries the
// version_number it belongs to so a stale stream can be filtered out.
export type ReviewEvent =
  | { type: 'comment.created'; version_number: number; commentId: string; comment: Comment }
  | { type: 'comment.updated'; version_number: number; commentId: string; comment: Comment }
  | { type: 'comment.resolved'; version_number: number; commentId: string }
  | { type: 'claude.question'; version_number: number; commentId: string; reply: Reply }
  | { type: 'claude.option'; version_number: number; commentId: string; reply: Reply }
  | { type: 'claude.clarification'; version_number: number; commentId: string; reply: Reply }
  | { type: 'status.changed'; version_number: number; status: ReviewStatus }
  | { type: 'submit'; version_number: number; feedbackPath: string }
  | { type: 'notification'; version_number: number; level: NotificationLevel; message: string };

export type ReviewEventType = ReviewEvent['type'];

export type NotificationLevel = 'info' | 'warn' | 'error';

export interface Notification {
  id: string;
  level: NotificationLevel;
  message: string;
  at: number;
}
