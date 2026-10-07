// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useSubmit } from '../lib/api';
import type { PullRequest, ReviewKind, SessionResponse } from '../lib/types';
import { pullRequest, section, session } from '../test/fixtures';
import { SubmitDialog } from './SubmitDialog';

function prSession(
  kind: ReviewKind,
  pullRequests: PullRequest[],
  sectionPRs = pullRequests.map((pr) => pr.number),
): SessionResponse {
  const base = session();
  return session({
    review: { ...base.review, kind },
    version: 3,
    sections: sectionPRs.map((prNumber) => section({ prNumber })),
    pullRequests,
  });
}

function Harness({ data }: { data: SessionResponse }) {
  const submit = useSubmit('slug');
  return <SubmitDialog session={data} submit={submit} open onClose={() => {}} />;
}

function radio(value: string): HTMLInputElement {
  return document.querySelector<HTMLInputElement>(`input[type=radio][value=${value}]`)!;
}

function button(text: string): HTMLButtonElement {
  return [...document.querySelectorAll<HTMLButtonElement>('button')].find((b) => b.textContent === text)!;
}

describe('SubmitDialog', () => {
  let root: Root;
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    fetchMock = vi.fn(async () => new Response(JSON.stringify({ ok: true, feedbackPath: '/f' })));
    vi.stubGlobal('fetch', fetchMock);
    const container = document.createElement('div');
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(() => {
    act(() => root.unmount());
    document.body.innerHTML = '';
    vi.unstubAllGlobals();
  });

  function render(data: SessionResponse) {
    const client = new QueryClient();
    act(() =>
      root.render(
        <QueryClientProvider client={client}>
          <Harness data={data} />
        </QueryClientProvider>,
      ),
    );
  }

  it('disables approve and request changes on a PR the viewer authored', () => {
    render(prSession('pr', [pullRequest({ number: 1 }), pullRequest({ number: 2, viewerIsAuthor: true })]));
    expect(radio('COMMENT').disabled).toBe(false);
    expect(radio('APPROVE').disabled).toBe(true);
    expect(radio('REQUEST_CHANGES').disabled).toBe(true);
    expect(document.body.textContent).toContain(
      'You authored #2; GitHub does not allow approving or requesting changes on your own pull request.',
    );
  });

  it('enables approve once the PR the viewer authored has left the stack', () => {
    render(prSession('pr', [pullRequest({ number: 1, viewerIsAuthor: true }), pullRequest({ number: 2 })], [2]));
    expect(radio('APPROVE').disabled).toBe(false);
    expect(radio('REQUEST_CHANGES').disabled).toBe(false);
  });

  it('submits the chosen verdict and summary against the displayed version', async () => {
    render(prSession('pr', [pullRequest()]));
    expect(radio('APPROVE').disabled).toBe(false);
    act(() => radio('APPROVE').click());
    const textarea = document.querySelector('textarea')!;
    act(() => {
      const setValue = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!;
      setValue.call(textarea, '  Ship it  ');
      textarea.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(async () => button('Submit review').click());
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe('/api/submit');
    expect(JSON.parse(init.body as string)).toEqual({
      reviewId: 'slug',
      versionNumber: 3,
      verdict: 'APPROVE',
      summary: 'Ship it',
    });
  });

  it('sends a local review to Claude without a verdict', async () => {
    render(prSession('local', []));
    expect(document.querySelector('input[type=radio]')).toBeNull();
    await act(async () => button('Send to Claude').click());
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toEqual({ reviewId: 'slug', summary: '' });
  });
});
