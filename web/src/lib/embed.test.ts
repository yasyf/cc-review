import { describe, expect, it } from 'vitest';
import { embedAction } from './embed';
import { section, session } from '../test/fixtures';

const PATCH = `diff --git a/app.js b/app.js
index 1111111..2222222 100644
--- a/app.js
+++ b/app.js
@@ -10,3 +10,4 @@
 const a = 1;
+const b = 2;
 const c = 3;
 const d = 4;
`;

const review = session({
  sections: [
    section({ sectionId: 'parent', prNumber: 6, files: [{ path: 'app.js', status: 'M' }], patchText: PATCH }),
    section({ sectionId: 'top', prNumber: 7, files: [{ path: 'app.js', status: 'M' }], patchText: PATCH }),
  ],
});

describe('embedAction', () => {
  it('asks the review assistant when no point in the change is named', () => {
    expect(embedAction(review, { id: '1', text: ' why a lock? ' })).toEqual({ kind: 'ask', prompt: 'why a lock?' });
  });

  it('comments on the named PR section of a file', () => {
    const action = embedAction(review, { id: '1', text: 'rename this', path: 'app.js', pr: 6 });
    expect(action).toMatchObject({ kind: 'comment', input: { sectionId: 'parent', filePath: 'app.js', subject: 'file', body: 'rename this' } });
  });

  it('comments on a changed line with its source', () => {
    const action = embedAction(review, { id: '1', text: 'why 2?', path: 'app.js', line: 11, pr: 7 });
    expect(action).toMatchObject({
      kind: 'comment',
      input: { sectionId: 'top', filePath: 'app.js', side: 'additions', body: 'why 2?', range: { start: 11, end: 11 }, lineContent: expect.stringContaining('const b = 2;') },
    });
  });

  it('refuses a line outside the diff, a file outside the review, and no words', () => {
    expect(embedAction(review, { id: '1', text: 'x', path: 'app.js', line: 90 })).toMatchObject({ kind: 'refuse' });
    expect(embedAction(review, { id: '1', text: 'x', path: 'other.js' })).toMatchObject({ kind: 'refuse' });
    expect(embedAction(review, { id: '1', text: '  ' })).toMatchObject({ kind: 'refuse' });
  });
});
