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
