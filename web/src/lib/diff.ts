import type { CodeViewItem, DiffLineAnnotation, FileDiffMetadata, SelectedLineRange } from '@pierre/diffs';
import { parsePatchFiles } from '@pierre/diffs';
import type { Comment, Side } from './types';

// A line annotation is either a persisted comment thread (body and replies are
// read live from the Query cache by CommentThread via the comment id) or the
// one in-flight comment composer.
export type AnnotationMeta =
  | { kind: 'thread'; commentId: string }
  | { kind: 'composer' };

// A comment being drafted, anchored to the selected lines of one file. `seq`
// increases on every open/replace so consecutive drafts never share an item
// version.
export interface ComposerDraft {
  filePath: string;
  range: SelectedLineRange;
  seq: number;
}

export type ReviewItem = CodeViewItem<AnnotationMeta>;

function anchorOf(comment: Comment): { side: Side; lineNumber: number } {
  return {
    side: comment.range.endSide ?? comment.side,
    lineNumber: comment.range.end,
  };
}

// Parse the patch once per version. Kept separate from annotation attachment so
// the FileDiffMetadata identity stays stable when only comments change — that
// prevents CodeView from re-tokenizing the diff on every comment/reply event.
export function parseFiles(patchText: string): FileDiffMetadata[] {
  return parsePatchFiles(patchText).flatMap((patch) => patch.files);
}

// Attach each comment as a line annotation on its file's item, plus the open
// composer draft as a transient annotation on its file. The composer is always
// appended last: annotation portals are keyed by array index, so keeping thread
// indices stable preserves their component state.
//
// `version` only changes when the annotation set changes (replies and body
// edits re-render the thread component via its own cache subscription). The
// parity scheme keeps consecutive states distinct: draft versions are odd and
// strictly increase with `seq`; non-draft versions are even and increase with
// the append-only comment count — so "draft open with N comments" → "draft
// closed with N+1 comments" never collides.
export function buildItems(
  files: readonly FileDiffMetadata[],
  comments: readonly Comment[],
  draft: ComposerDraft | null,
): ReviewItem[] {
  const byFile = new Map<string, DiffLineAnnotation<AnnotationMeta>[]>();
  for (const comment of comments) {
    const anchor = anchorOf(comment);
    const list = byFile.get(comment.filePath) ?? [];
    list.push({ ...anchor, metadata: { kind: 'thread', commentId: comment.id } });
    byFile.set(comment.filePath, list);
  }

  return files.map((file): ReviewItem => {
    const threads = byFile.get(file.name) ?? [];
    const fileDraft = draft?.filePath === file.name ? draft : null;
    const annotations = fileDraft
      ? [
          ...threads,
          {
            side: fileDraft.range.endSide ?? fileDraft.range.side ?? 'additions',
            lineNumber: fileDraft.range.end,
            metadata: { kind: 'composer' } satisfies AnnotationMeta,
          },
        ]
      : threads;
    return {
      id: file.name,
      type: 'diff',
      fileDiff: file,
      annotations,
      version: fileDraft ? 2 * (threads.length + fileDraft.seq) + 1 : 2 * threads.length,
    };
  });
}

// Resolve the source text of a selected line so it can ride along with a new
// comment. Each hunk maps a contiguous run of line numbers
// (start .. start+count) onto a slice of the side's line array starting at
// lineIndex, so the offset within the hunk indexes the content directly.
// (iterateOverDiff is not exported from the package entry, hence this direct
// hunk walk.)
export function lineContentAt(fileDiff: FileDiffMetadata, side: Side, lineNumber: number): string {
  for (const hunk of fileDiff.hunks) {
    if (side === 'additions') {
      const offset = lineNumber - hunk.additionStart;
      if (offset >= 0 && offset < hunk.additionCount) {
        return fileDiff.additionLines[hunk.additionLineIndex + offset];
      }
    } else {
      const offset = lineNumber - hunk.deletionStart;
      if (offset >= 0 && offset < hunk.deletionCount) {
        return fileDiff.deletionLines[hunk.deletionLineIndex + offset];
      }
    }
  }
  return '';
}
