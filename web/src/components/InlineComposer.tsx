import { useState } from 'react';
import type { FileDiffMetadata } from '@pierre/diffs';
import { useCreateComment } from '../lib/api';
import { lineContentAt } from '../lib/diff';
import type { ComposerDraft } from '../lib/diff';
import { composerDraftKey, readDraft, writeDraft } from '../lib/drafts';
import { useReview } from '../lib/review-context';
import type { LineRange, Side } from '../lib/types';

export function InlineComposer({
  draft,
  fileDiff,
  versionId,
  onClose,
}: {
  draft: ComposerDraft;
  fileDiff: FileDiffMetadata;
  versionId: string;
  onClose(): void;
}) {
  const { slug } = useReview();
  const createComment = useCreateComment(slug);
  // Rehydrate across portal remounts (annotation index shifts, virtualizer
  // releases); closeDraft owns clearing the stored text.
  const [body, setBody] = useState(() => readDraft(composerDraftKey));

  function updateBody(text: string) {
    setBody(text);
    writeDraft(composerDraftKey, text);
  }

  const side: Side = draft.range.endSide ?? draft.range.side ?? 'additions';

  function submit() {
    const text = body.trim();
    if (!text) return;
    const range: LineRange = {
      start: draft.range.start,
      end: draft.range.end,
      ...(draft.range.side ? { startSide: draft.range.side } : {}),
      ...(draft.range.endSide ? { endSide: draft.range.endSide } : {}),
    };
    createComment.mutate(
      {
        versionId,
        filePath: draft.filePath,
        side,
        range,
        lineContent: lineContentAt(fileDiff, side, draft.range.end),
        body: text,
      },
      { onSuccess: onClose },
    );
  }

  return (
    <div className="composer">
      <div className="composer-head">
        <span className="composer-range">
          L{draft.range.start}
          {draft.range.end !== draft.range.start ? `–${draft.range.end}` : ''}
        </span>
      </div>
      <textarea
        autoFocus
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
