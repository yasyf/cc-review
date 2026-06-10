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
export type AnsweredVia = 'web' | 'askuserquestion';

export interface AskOption {
  label: string;
  description?: string;
  preview?: string;
}

export interface Ask {
  header?: string;
  multiSelect?: boolean;
  options: AskOption[];
}

export interface AskAnswer {
  selected: string[];
  other?: string;
  notes?: string;
}

export interface Review {
  id: string;
  status: ReviewStatus;
  repoRoot: string;
  branch: string;
  createdAt: string;
}

interface ReplyBase {
  id: string;
  commentId: string;
  origin: Origin;
  body: string;
  createdAt: string;
}

// Claude streams these reply kinds under a comment; a user answer is kind 'answer'.
export type Reply =
  | (ReplyBase & { kind: 'question' | 'clarification' | 'note' | 'answer' })
  | (ReplyBase & {
      kind: 'ask';
      ask: Ask;
      answered?: boolean;
      askAnswer?: AskAnswer;
      answeredVia?: AnsweredVia;
    });

export type ReplyKind = Reply['kind'];

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

// Mirrors the daemon's gitdiff.FileChange. The diff itself is parsed from
// patchText; this list is only used for the file count, so it carries just the
// git name-status fields the daemon actually emits.
export interface FileMeta {
  path: string;
  old_path?: string;
  status: string; // git name-status code: A | M | D | R | C | T
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

// The SSE payload is a tagged union on `type`. Every frame carries the
// version_number it belongs to so a stale stream can be filtered out.
export type ReviewEvent =
  | { type: 'comment.created'; version_number: number; commentId: string; comment: Comment }
  | { type: 'comment.updated'; version_number: number; commentId: string; comment: Comment }
  | { type: 'comment.resolved'; version_number: number; commentId: string }
  | { type: 'claude.question'; version_number: number; commentId: string; reply: Reply }
  | { type: 'claude.ask'; version_number: number; commentId: string; reply: Reply }
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
