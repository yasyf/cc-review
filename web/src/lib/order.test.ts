import { describe, expect, it } from 'vitest';
import { chapterFile, section, session } from '../test/fixtures';
import { chapterFileOf, fileOrder, riskOf, sectionTodoGroups, todoGroups } from './order';
import type { Organization } from './types';

const organization: Organization = {
  overview: null,
  chapters: [
    { title: 'docs', summary: '', files: [chapterFile('README.md', 'mechanical')] },
    { title: 'core', summary: '', files: [chapterFile('src/b.ts', 'medium'), chapterFile('src/a.ts', 'high')] },
    { title: 'tests', summary: '', files: [chapterFile('src/a.test.ts', 'low'), chapterFile('src/a.ts', 'low')] },
  ],
};

const files = ['src/a.ts', 'src/b.ts', 'README.md', 'src/a.test.ts', 'go.mod'].map((path) => ({ path, status: 'M' }));

describe('chapterFileOf / riskOf', () => {
  it('returns the first chapter entry for a path', () => {
    expect(chapterFileOf(organization, 'src/a.ts')?.risk).toBe('high');
    expect(riskOf(organization, 'README.md')).toBe('mechanical');
  });

  it('returns null without an organization or for unranked paths', () => {
    expect(chapterFileOf(null, 'src/a.ts')).toBeNull();
    expect(riskOf(organization, 'go.mod')).toBeNull();
  });
});

describe('todoGroups', () => {
  it('ranks chapters scariest-first and dedupes paths across chapters', () => {
    const groups = todoGroups(organization, new Set(files.map((f) => f.path)));
    expect(groups.map((g) => [g.title, g.files.map((f) => f.path)])).toEqual([
      ['core', ['src/a.ts', 'src/b.ts']],
      ['tests', ['src/a.test.ts']],
      ['docs', ['README.md']],
    ]);
  });

  it('drops chapters whose files are absent from the patch', () => {
    const groups = todoGroups(organization, new Set(['README.md']));
    expect(groups.map((g) => g.title)).toEqual(['docs']);
  });
});

describe('sectionTodoGroups', () => {
  it('re-ranks across sections and breaks risk ties by section position', () => {
    const trunk = section({
      sectionKey: 'feat/a',
      branch: 'feat/a',
      pending: false,
      files: [{ path: 'x.ts', status: 'M' }],
      organization: { overview: null, chapters: [{ title: 'x', summary: '', files: [chapterFile('x.ts', 'high')] }] },
    });
    const tip = section({
      sectionKey: 'feat/b',
      branch: 'feat/b',
      pending: false,
      files: [{ path: 'y.ts', status: 'M' }, { path: 'z.ts', status: 'M' }],
      organization: {
        overview: null,
        chapters: [
          { title: 'y', summary: '', files: [chapterFile('y.ts', 'high')] },
          { title: 'z', summary: '', files: [chapterFile('z.ts', 'low')] },
        ],
      },
    });
    expect(sectionTodoGroups([trunk, tip]).map((g) => [g.title, g.branch])).toEqual([
      ['x', 'feat/a'],
      ['y', 'feat/b'],
      ['z', 'feat/b'],
    ]);
  });
});

describe('fileOrder', () => {
  const s = session({ sections: [section({ files, organization })] });

  it.each([
    ['default', ['src/a.ts', 'src/b.ts', 'README.md', 'src/a.test.ts', 'go.mod']],
    ['story', ['README.md', 'src/b.ts', 'src/a.ts', 'src/a.test.ts', 'go.mod']],
    ['todo', ['src/a.ts', 'src/b.ts', 'src/a.test.ts', 'README.md', 'go.mod']],
  ] as const)('orders %s mode with unorganized files trailing', (mode, expected) => {
    const order = fileOrder(s, mode);
    expect([...order.entries()].sort((a, b) => a[1] - b[1]).map(([id]) => id)).toEqual(
      expected.map((path) => `f::${path}`),
    );
  });

  it('is section-major and keys the same path per section', () => {
    const stacked = session({
      sections: [
        section({ sectionKey: 'feat/a', files: [{ path: 'a.ts', status: 'M' }] }),
        section({ sectionKey: '', files: [{ path: 'a.ts', status: 'M' }] }),
      ],
    });
    expect([...fileOrder(stacked, 'default').entries()]).toEqual([
      ['f:feat/a:a.ts', 0],
      ['f::a.ts', 1],
    ]);
  });
});
