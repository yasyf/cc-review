import type { CodeViewItem, DiffLineAnnotation, FileDiffMetadata, SelectedLineRange } from '@pierre/diffs';
import { parsePatchFiles } from '@pierre/diffs';
import type { Comment, Section, Side } from './types';

// A line annotation is either a persisted comment thread (body and replies are
// read live from the Query cache by CommentThread via the comment id) or the
// one in-flight comment composer.
export type AnnotationMeta =
  | { kind: 'thread'; commentId: string }
  | { kind: 'composer' };

// A file within the review, identified by its section (`sectionKey === ''` for
// the pending working-tree section) and path. The same path can appear in two
// sections, so this pair — not the bare path — is the wire identity.
export interface FileRef {
  sectionKey: string;
  path: string;
}

// The CodeView item id encodes a FileRef or a section banner. Branch names
// cannot contain ':' and '' is a valid section key, so the first ':' after the
// 'f:' prefix always delimits key from path and the parse is total.
export type ItemRef =
  | { kind: 'file'; sectionKey: string; path: string }
  | { kind: 'banner'; sectionKey: string };

export function fileItemId(sectionKey: string, path: string): string {
  return `f:${sectionKey}:${path}`;
}

export function bannerItemId(sectionKey: string): string {
  return `s:${sectionKey}`;
}

export function parseItemId(id: string): ItemRef {
  if (id[0] === 's') return { kind: 'banner', sectionKey: id.slice(2) };
  const rest = id.slice(2);
  const sep = rest.indexOf(':');
  return { kind: 'file', sectionKey: rest.slice(0, sep), path: rest.slice(sep + 1) };
}

// The section a comment lands on: pending → '', else its branch.
export function commentSectionKey(comment: Pick<Comment, 'pending' | 'branch'>): string {
  return comment.pending ? '' : comment.branch;
}

export function commentItemId(comment: Comment): string {
  return fileItemId(commentSectionKey(comment), comment.filePath);
}

// A comment being drafted, anchored to the selected lines of one section's file.
// `seq` increases on every open/replace so consecutive drafts never share an
// item version.
export interface ComposerDraft {
  sectionId: string;
  sectionKey: string;
  filePath: string;
  range: SelectedLineRange;
  seq: number;
}

export type ReviewItem = CodeViewItem<AnnotationMeta>;

// A section paired with its parsed diff; the input unit for buildItems.
export interface SectionFiles {
  section: Section;
  files: readonly FileDiffMetadata[];
}

function anchorOf(comment: Comment): { side: Side; lineNumber: number } {
  return {
    side: comment.range.endSide ?? comment.side,
    lineNumber: comment.range.end,
  };
}

// Parse the patch once per section. Kept separate from annotation attachment so
// the FileDiffMetadata identity stays stable when only comments change — that
// prevents CodeView from re-tokenizing the diff on every comment/reply event.
export function parseFiles(patchText: string): FileDiffMetadata[] {
  return parsePatchFiles(patchText).flatMap((patch) => patch.files);
}

// The banner is a synthetic collapsed empty file item; its header chrome is
// rendered by SectionHeader through renderHeaderMetadata (which self-subscribes
// to the cache), so the item itself never changes and pins version 0.
function bannerItem(section: Section): ReviewItem {
  return {
    id: bannerItemId(section.sectionKey),
    type: 'file',
    file: { name: section.pending ? 'Working tree' : section.branch, contents: '' },
    annotations: [],
    collapsed: true,
    version: 0,
  };
}

// Interleave each section's file items in position order, prefixed by a banner
// item when banners are shown (only for a multi-section review, so a flat
// review's item list is exactly today's — one section, no banner). Within a
// section, files sort by the view-mode order map and comments/composer attach
// as line annotations, mirroring the single-diff behaviour.
//
// The version parity scheme is per file item and unchanged: draft versions are
// odd and strictly increase with `seq`; non-draft versions are even and
// increase with the append-only comment count; collapse folds in as a low bit
// (version' = base * 2 + (collapsed ? 1 : 0)). Ids and every key (order,
// expandOverrides, autoCollapse) are itemIds so the same path in two sections
// never collides.
export function buildItems(
  sections: readonly SectionFiles[],
  comments: readonly Comment[],
  draft: ComposerDraft | null,
  order: ReadonlyMap<string, number>,
  hideReviewed: boolean,
  expandOverrides: ReadonlySet<string>,
  autoCollapse: ReadonlySet<string>,
  showBanners: boolean,
): ReviewItem[] {
  const byItem = new Map<string, DiffLineAnnotation<AnnotationMeta>[]>();
  for (const comment of comments) {
    const anchor = anchorOf(comment);
    const id = commentItemId(comment);
    const list = byItem.get(id) ?? [];
    list.push({ ...anchor, metadata: { kind: 'thread', commentId: comment.id } });
    byItem.set(id, list);
  }

  const draftItemId = draft ? fileItemId(draft.sectionKey, draft.filePath) : null;
  const items: ReviewItem[] = [];

  for (const { section, files } of sections) {
    if (showBanners) items.push(bannerItem(section));

    const visible = files.filter((file) => {
      const state = section.fileStates[file.name];
      if (state?.hidden) return false;
      if (hideReviewed && state?.reviewed && !expandOverrides.has(fileItemId(section.sectionKey, file.name))) {
        return false;
      }
      return true;
    });
    visible.sort(
      (a, b) =>
        (order.get(fileItemId(section.sectionKey, a.name)) ?? Infinity) -
        (order.get(fileItemId(section.sectionKey, b.name)) ?? Infinity),
    );

    for (const file of visible) {
      const id = fileItemId(section.sectionKey, file.name);
      const threads = byItem.get(id) ?? [];
      const fileDraft = draftItemId === id ? draft : null;
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
      const collapsed =
        !fileDraft &&
        ((section.fileStates[file.name]?.reviewed ?? false) || autoCollapse.has(id)) &&
        !expandOverrides.has(id);
      const base = fileDraft ? 2 * (threads.length + fileDraft.seq) + 1 : 2 * threads.length;
      items.push({
        id,
        type: 'diff',
        fileDiff: file,
        annotations,
        collapsed,
        version: base * 2 + (collapsed ? 1 : 0),
      });
    }
  }

  return items;
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
