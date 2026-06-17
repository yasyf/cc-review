import type { Risk } from '../lib/types';

// risk/rationale are absent for patch files the organization hasn't ranked yet
// (the todo view's "Unsorted" pseudo-group).
export interface RowFile {
  path: string;
  risk?: Risk;
  rationale?: string;
  focus?: string;
}

export function FileRow({
  file,
  reviewed,
  commentCount,
  needsReply,
  onSelect,
  onToggle,
}: {
  file: RowFile;
  reviewed: boolean;
  commentCount: number;
  needsReply?: boolean;
  onSelect(): void;
  onToggle(): void;
}) {
  const name = file.path.split('/').pop();
  return (
    <div
      className={`chapter-row${reviewed ? ' chapter-row-reviewed' : ''}`}
      title={[file.focus && `Focus: ${file.focus}`, file.rationale].filter(Boolean).join('\n')}
    >
      <input type="checkbox" checked={reviewed} onChange={onToggle} aria-label="Viewed" />
      <button type="button" className="chapter-row-path" onClick={onSelect}>
        {name}
      </button>
      {file.risk ? <span className={`risk-chip risk-${file.risk}`}>{file.risk}</span> : null}
      {needsReply ? <span className="needs-reply-chip">needs reply</span> : null}
      {commentCount > 0 ? <span className="comment-card-count">{commentCount}</span> : null}
    </div>
  );
}
