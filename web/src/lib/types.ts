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

export interface FileState {
  reviewed: boolean;
  hidden: boolean;
}

export type Risk = 'high' | 'medium' | 'low' | 'mechanical';

export interface ChapterFile {
  path: string;
  risk: Risk;
  rationale: string;
}

export interface Chapter {
  title: string;
  summary: string;
  files: ChapterFile[];
}

export interface Organization {
  overview: string | null;
  chapters: Chapter[];
}

export type AiRequestSource = 'user' | 'system';
export type AiRequestStatus = 'pending' | 'working' | 'done' | 'failed' | 'undone';

export interface AiRequestUnmatched {
  pattern: string;
  why: string;
}

// Per-path state snapshot recorded when Claude applies a batch; `prior` powers
// one-click undo on the daemon side.
export interface AiRequestChange {
  path: string;
  reason: string;
  prior: { reviewed: boolean; hidden: boolean; fingerprint: string };
  applied: { reviewed: boolean; hidden: boolean };
}

export interface AiRequest {
  id: string;
  source: AiRequestSource;
  prompt: string;
  status: AiRequestStatus;
  summary: string;
  unmatched: AiRequestUnmatched[];
  changes: AiRequestChange[];
  createdAt: string;
  updatedAt: string;
}

export interface Turn {
  id: string;
  sessionId: string;
  prompt: string;
  interrupted: boolean;
  startedAt: number;
  endedAt: number;
}

// New-side 1-based inclusive added-line range; a missing turnId means a
// manual or pre-existing edit.
export interface AttributionRange {
  start: number;
  end: number;
  turnId?: string;
}

export type DecisionAction = 'allow' | 'block' | 'warn' | 'nudge' | 'note';

// One decision-ledger row inside a turn window: a hook or gate verdict
// recorded by a cc-family tool (cc-review's own gate, captain-hook, …).
export interface TurnDecision {
  tsMs: number;
  source: string;
  kind: string;
  action: DecisionAction;
  toolName: string;
  message: string;
}

// One tool call of a turn window; field names come straight from the
// cc-transcript.slice/1 contract the daemon passes through.
export interface ProvenanceItem {
  event_uuid: string;
  ts_ms: number;
  tool_name: string;
  summary: string;
  file_path: string;
}

// provenance_unavailable means the slice degraded (binary absent, transcript
// expired); the empty list is then a "don't know", not a "nothing happened".
export interface ProvenanceResponse {
  provenance: ProvenanceItem[];
  provenance_unavailable: boolean;
}

export interface SessionResponse {
  review: Review;
  version: number;
  // Stable id of this version row — referenced when creating a comment.
  versionId: string;
  files: FileMeta[];
  patchText: string;
  comments: Comment[];
  // Keyed by path, filtered to the displayed version's files.
  fileStates: Record<string, FileState>;
  // For the displayed version only; null until Claude submits one.
  organization: Organization | null;
  // Newest first.
  aiRequests: AiRequest[];
  // Ordered; display seq = index + 1.
  turns: Turn[];
  // Keyed by path; ranges sorted by start within each file.
  attributions: Record<string, AttributionRange[]>;
  // Keyed by turn id; every listed turn has an entry, possibly empty.
  turnActivity: Record<string, TurnDecision[]>;
  claudeConnected: boolean;
  // Max event seq when the session was fetched, int64-as-string ("0" when no
  // events). The SSE handler toasts only frames newer than this, so the
  // cursor-0 replay patches state without replaying notifications.
  latestEventSeq: string;
}

// `file.states` entries are absolute per-path values, never deltas: the
// browser replays the whole event log from cursor 0 on every load, so every
// payload must be idempotent.
export interface FileStateEventEntry {
  path: string;
  reviewed: boolean;
  hidden: boolean;
  reason?: string;
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
  | { type: 'notification'; version_number: number; level: NotificationLevel; message: string }
  | {
      type: 'file.states';
      version_number: number;
      states: FileStateEventEntry[];
      aiRequestId?: string;
      undoOf?: string;
    }
  | { type: 'version.created'; version_number: number }
  | { type: 'ai.request.created'; version_number: number; request: AiRequest }
  | { type: 'ai.request.updated'; version_number: number; request: AiRequest }
  | { type: 'organization.updated'; version_number: number; organization: Organization }
  | { type: 'channel.changed'; version_number: number; connected: boolean };

export type ReviewEventType = ReviewEvent['type'];

export type NotificationLevel = 'info' | 'warn' | 'error';

export interface Notification {
  id: string;
  level: NotificationLevel;
  message: string;
  at: number;
}
