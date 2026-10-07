import { describe, expect, it } from 'vitest';
import { buildItems, parseFiles } from './items';
import type { AnnotationMeta } from './items';
import { comment, section } from '../../test/fixtures';
import type { Comment } from '../types';

const PATCH = 'diff --git a/a.ts b/a.ts\n--- a/a.ts\n+++ b/a.ts\n@@ -1,2 +1,3 @@\n const a = 1;\n+const b = 2;\n const c = 3;\n';

function build(inline: Comment[], strip: Comment[]) {
  const sec = section({ sectionKey: '', pending: true });
  return buildItems([{ section: sec, files: parseFiles(PATCH) }], inline, strip, null, new Map(), false, new Set(), new Set());
}

describe('buildItems strip threads', () => {
  const fileLevel = comment({ id: 'f1', subject: 'file', range: { start: 0, end: 0 } });
  const inline = comment({ id: 'l1', range: { start: 2, end: 2 } });

  it('anchors file-level threads in one strip at the first hunk line', () => {
    const [item] = build([inline], [fileLevel]);
    const metas = (item.annotations ?? []).map((a) => a.metadata as AnnotationMeta);
    expect(metas).toEqual([
      { kind: 'strip', commentIds: ['f1'] },
      { kind: 'thread', commentId: 'l1' },
    ]);
    expect(item.annotations?.[0]).toMatchObject({ side: 'additions', lineNumber: 1 });
  });

  it('changes the version when a thread moves from inline to the strip', () => {
    const outdated = { ...inline, outdated: true };
    const [before] = build([inline], [fileLevel]);
    const [after] = build([], [fileLevel, outdated]);
    expect(after.version).not.toBe(before.version);
  });
});
