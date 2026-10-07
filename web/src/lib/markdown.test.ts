// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { renderMarkdown } from './markdown';

function render(source: string): HTMLElement {
  const root = document.createElement('div');
  root.innerHTML = renderMarkdown(source);
  return root;
}

describe('renderMarkdown sanitization', () => {
  it.each([
    ['script tag', '<script>alert(1)</script>hi', 'script'],
    ['inline handler', '<img src="x" onerror="alert(1)">', '[onerror]'],
    ['iframe', '<iframe src="https://evil.test"></iframe>', 'iframe'],
  ])('strips %s', (_name, source, selector) => {
    expect(render(source).querySelector(selector)).toBeNull();
  });

  it('drops javascript: hrefs', () => {
    const link = render('[x](javascript:alert(1))').querySelector('a');
    expect(link?.getAttribute('href')).toBeNull();
  });

  it('opens links in a new tab without an opener', () => {
    const link = render('[docs](https://example.com)').querySelector('a');
    expect(link?.getAttribute('href')).toBe('https://example.com');
    expect(link?.getAttribute('target')).toBe('_blank');
    expect(link?.getAttribute('rel')).toBe('noopener noreferrer');
  });

  it('renders GFM emphasis, lists and line breaks', () => {
    const root = render('**bold**\nnext\n\n- one\n- two');
    expect(root.querySelector('strong')?.textContent).toBe('bold');
    expect(root.querySelector('br')).not.toBeNull();
    expect([...root.querySelectorAll('li')].map((li) => li.textContent)).toEqual(['one', 'two']);
  });
});

describe('renderMarkdown suggestion fences', () => {
  it('renders a read-only suggested-change block with one row per line', () => {
    const root = render('Try this:\n\n```suggestion\nconst a = 1;\nconst b = <T>(x: T) => x;\n```');
    const block = root.querySelector('.md-suggestion');
    expect(block?.querySelector('.md-suggestion-head')?.textContent).toBe('Suggested change');
    expect([...root.querySelectorAll('.md-suggestion-line')].map((line) => line.textContent)).toEqual([
      'const a = 1;',
      'const b = <T>(x: T) => x;',
    ]);
    expect(root.querySelector('textarea, button, input')).toBeNull();
  });

  it('escapes markup inside a suggestion', () => {
    const root = render('```suggestion\n<img src=x onerror=alert(1)>\n```');
    expect(root.querySelector('img')).toBeNull();
    expect(root.querySelector('.md-suggestion-line')?.textContent).toBe('<img src=x onerror=alert(1)>');
  });

  it('renders an empty suggestion as a removal', () => {
    const root = render('```suggestion\n```');
    expect(root.querySelector('.md-suggestion-empty')?.textContent).toBe('Remove the selected lines');
    expect(root.querySelector('.md-suggestion-line')).toBeNull();
  });

  it('leaves other fenced code as a plain code block', () => {
    const root = render('```ts\nconst a = 1;\n```');
    expect(root.querySelector('.md-suggestion')).toBeNull();
    expect(root.querySelector('pre code')?.textContent).toBe('const a = 1;\n');
  });
});

describe('renderMarkdown GitHub extensions', () => {
  it('renders an alert as a callout', () => {
    const root = render('> [!WARNING]\n> This pull request is not mergeable.');
    const alert = root.querySelector('.md-alert-warning');
    expect(alert?.querySelector('.md-alert-title')?.textContent).toBe('Warning');
    expect(alert?.textContent).toContain('This pull request is not mergeable.');
    expect(alert?.textContent).not.toContain('[!WARNING]');
    expect(root.querySelector('blockquote')).toBeNull();
  });

  it('keeps a plain blockquote', () => {
    expect(render('> just a quote').querySelector('blockquote')?.textContent).toContain('just a quote');
  });

  it('drops HTML comments, including unterminated ones', () => {
    const root = render('before <!-- eyJibG9iIjoxfQ== --> after\n\n<!-- trailing');
    expect(root.innerHTML).not.toContain('eyJibG9i');
    expect(root.textContent).toContain('before');
    expect(root.textContent).toContain('after');
    expect(root.textContent).not.toContain('trailing');
  });

  it('keeps comment syntax inside code', () => {
    const root = render('Use `<!--` to open one.\n\n```html\n<!-- kept -->\n```\n\nTail text.');
    expect(root.querySelector('p code')?.textContent).toBe('<!--');
    expect(root.querySelector('pre code')?.textContent).toContain('<!-- kept -->');
    expect(root.textContent).toContain('Tail text.');
  });

  it.each([
    ['img', '<img src="https://example.com/i.png" width="14">'],
    ['kbd', '<kbd>⌘K</kbd>'],
    ['sub', 'H<sub>2</sub>O'],
    ['details', '<details><summary>More</summary>hidden</details>'],
  ])('keeps sanitized %s', (selector, source) => {
    expect(render(source).querySelector(selector)).not.toBeNull();
  });
});
