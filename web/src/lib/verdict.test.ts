import { describe, expect, it } from 'vitest';
import { pullRequest, section } from '../test/fixtures';
import { verdictOptions } from './verdict';

const own = (number: number) => pullRequest({ number, viewerIsAuthor: true });
const theirs = (number: number) => pullRequest({ number });

describe('verdictOptions', () => {
  it.each([
    { name: 'no pull requests', prs: [], sections: [], reason: null },
    { name: 'someone else authored every PR', prs: [theirs(1), theirs(2)], sections: [1, 2], reason: null },
    {
      name: 'viewer authored one PR',
      prs: [theirs(1), own(2)],
      sections: [1, 2],
      reason:
        'You authored #2; GitHub does not allow approving or requesting changes on your own pull request.',
    },
    {
      name: 'viewer authored several PRs',
      prs: [own(3), own(4)],
      sections: [3, 4],
      reason:
        'You authored #3, #4; GitHub does not allow approving or requesting changes on your own pull request.',
    },
    { name: 'viewer authored a PR that left the stack', prs: [own(1), theirs(2)], sections: [2], reason: null },
  ])('$name', ({ prs, sections, reason }) => {
    expect(verdictOptions(sections.map((prNumber) => section({ prNumber })), prs)).toEqual([
      { value: 'COMMENT', label: 'Comment', disabledReason: null },
      { value: 'APPROVE', label: 'Approve', disabledReason: reason },
      { value: 'REQUEST_CHANGES', label: 'Request changes', disabledReason: reason },
    ]);
  });
});
