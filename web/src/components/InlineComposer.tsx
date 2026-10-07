import { useState } from 'react';
import type { FileDiffMetadata } from '@pierre/diffs';
import { useCreateComment, useSession } from '../lib/api';
import { lineContentAt, rangeInOneHunk } from '../lib/diff/items';
import type { ComposerDraft } from '../lib/diff/items';
import { composerDraftKey, readDraft, writeDraft } from '../lib/drafts';
import { useReview } from '../lib/review-context';
import type { LineRange, Side } from '../lib/types';
import { Button } from './ui/Button';
import { Tooltip } from './ui/Tooltip';

export function InlineComposer({
  draft,
  fileDiff,
  onClose,
}: {
  draft: ComposerDraft;
  fileDiff: FileDiffMetadata;
  onClose(): void;
}) {
  const { slug, version } = useReview();
  const { data } = useSession(slug, version);
  const createComment = useCreateComment(slug);
  // Rehydrate across portal remounts (annotation index shifts, virtualizer
  // releases); closeDraft owns clearing the stored text.
  const [body, setBody] = useState(() => readDraft(composerDraftKey));

  function updateBody(text: string) {
    setBody(text);
    writeDraft(composerDraftKey, text);
  }

  const side: Side = draft.range.endSide ?? draft.range.side ?? 'additions';
  const startSide: Side = draft.range.side ?? side;
  const outsideHunk =
    data?.review.kind === 'pr' &&
    !rangeInOneHunk(fileDiff, { start: draft.range.start, end: draft.range.end, side: startSide, endSide: side });

  function submit() {
    const text = body.trim();
    if (!text) return;
    const range: LineRange = {
      start: draft.range.start,
      end: draft.range.end,
      ...(draft.range.side ? { startSide: draft.range.side } : {}),
      ...(draft.range.endSide ? { endSide: draft.range.endSide } : {}),
    };
    createComment.mutate({
      sectionId: draft.sectionId,
      filePath: draft.filePath,
      side,
      range,
      lineContent: lineContentAt(fileDiff, side, draft.range.end),
      body: text,
    });
    // Close eagerly, like the reply box: the comment.created SSE event shifts
    // this portal's annotation index and remounts it, which drops any
    // mutate-level onSuccess before it can fire.
    onClose();
  }

  return (
    <div className="composer">
      <div className="composer-head">
        <span className="composer-range">
          L{draft.range.start}
          {draft.range.end !== draft.range.start ? `–${draft.range.end}` : ''}
        </span>
      </div>
      {outsideHunk ? (
        <div className="composer-blocked" role="note">
          GitHub only accepts review comments on lines inside one diff hunk; this selection includes expanded
          context. Select changed or nearby lines, or comment on the whole file from its header.
        </div>
      ) : null}
      <textarea
        autoFocus
        disabled={outsideHunk}
        value={body}
        placeholder="Leave a comment…"
        onChange={(e) => updateBody(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
            e.preventDefault();
            submit();
          }
          if (e.key === 'Escape') onClose();
        }}
      />
      <div className="composer-actions">
        <Button variant="ghost" onClick={onClose}>
          Cancel
        </Button>
        {outsideHunk ? (
          <Tooltip label="GitHub rejects review comments on expanded context lines">
            <span className="disabled-tooltip-anchor" tabIndex={0}>
              <Button variant="primary" disabled>
                Add comment
              </Button>
            </span>
          </Tooltip>
        ) : (
          <Button variant="primary" disabled={!body.trim()} onClick={submit}>
            Add comment
          </Button>
        )}
      </div>
    </div>
  );
}
