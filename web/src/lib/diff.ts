import type { CodeViewItem, DiffLineAnnotation, FileDiffMetadata } from '@pierre/diffs';
import { parsePatchFiles } from '@pierre/diffs';
import type { Comment, Side } from './types';

// The per-line annotation only needs to locate its thread; the comment body and
// replies are read live from the Query cache by CommentThread via this id.
export interface ThreadMeta {
  commentId: string;
}

export type ReviewItem = CodeViewItem<ThreadMeta>;

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

// Attach each comment as a line annotation on its file's item. `version` is the
// annotation count: it changes only when a comment is added or removed, which is
// the sole event that mutates an item's annotation set (replies and body edits
// re-render the thread component via its own cache subscription, so they need no
// item-level re-render).
export function buildItems(files: readonly FileDiffMetadata[], comments: readonly Comment[]): ReviewItem[] {
  const byFile = new Map<string, DiffLineAnnotation<ThreadMeta>[]>();
  for (const comment of comments) {
    const anchor = anchorOf(comment);
    const list = byFile.get(comment.filePath) ?? [];
    list.push({ ...anchor, metadata: { commentId: comment.id } });
    byFile.set(comment.filePath, list);
  }

  return files.map((file): ReviewItem => {
    const annotations = byFile.get(file.name) ?? [];
    return {
      id: file.name,
      type: 'diff',
      fileDiff: file,
      annotations,
      version: annotations.length,
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
