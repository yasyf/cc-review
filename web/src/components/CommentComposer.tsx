import { useState } from 'react';
import type { CodeViewLineSelection, FileDiffMetadata } from '@pierre/diffs';
import { useCreateComment } from '../lib/api';
import { lineContentAt } from '../lib/diff';
import { useReview } from '../lib/review-context';
import type { LineRange, Side } from '../lib/types';

export function CommentComposer({
  selection,
  fileDiff,
  versionId,
  onClose,
}: {
  selection: CodeViewLineSelection;
  fileDiff: FileDiffMetadata;
  versionId: string;
  onClose(): void;
}) {
  const { reviewId, token } = useReview();
  const createComment = useCreateComment(reviewId, token);
  const [body, setBody] = useState('');

  const side: Side = selection.range.endSide ?? selection.range.side ?? 'additions';

  function submit() {
    const text = body.trim();
    if (!text) return;
    const range: LineRange = {
      start: selection.range.start,
      end: selection.range.end,
      ...(selection.range.side ? { startSide: selection.range.side } : {}),
      ...(selection.range.endSide ? { endSide: selection.range.endSide } : {}),
    };
    createComment.mutate(
      {
        versionId,
        filePath: selection.id,
        side,
        range,
        lineContent: lineContentAt(fileDiff, side, selection.range.end),
        body: text,
      },
      { onSuccess: onClose },
    );
  }

  return (
    <div className="composer">
      <div className="composer-head">
        <code>{selection.id}</code>
        <span className="composer-range">
          {side} L{selection.range.start}
          {selection.range.end !== selection.range.start ? `–${selection.range.end}` : ''}
        </span>
      </div>
      <textarea
        autoFocus
        value={body}
        placeholder="Leave a comment…"
        onChange={(e) => setBody(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
            e.preventDefault();
            submit();
          }
          if (e.key === 'Escape') onClose();
        }}
      />
      <div className="composer-actions">
        <button type="button" onClick={onClose}>
          Cancel
        </button>
        <button type="button" className="primary" disabled={createComment.isPending || !body.trim()} onClick={submit}>
          Add comment
        </button>
      </div>
    </div>
  );
}
