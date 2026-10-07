import DOMPurify from 'dompurify';
import { Marked } from 'marked';

const HTML_ESCAPES: Record<string, string> = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
};

function escapeHtml(text: string): string {
  return text.replace(/[&<>"']/g, (ch) => HTML_ESCAPES[ch]);
}

function suggestionBlock(text: string): string {
  const body =
    text === ''
      ? '<div class="md-suggestion-empty">Remove the selected lines</div>'
      : `<pre class="md-suggestion-body">${text
          .split('\n')
          .map((line) => `<span class="md-suggestion-line">${escapeHtml(line)}</span>`)
          .join('')}</pre>`;
  return `<div class="md-suggestion"><div class="md-suggestion-head">Suggested change</div>${body}</div>`;
}

const markdown = new Marked({
  gfm: true,
  breaks: true,
  renderer: {
    code({ text, lang }) {
      return lang === 'suggestion' ? suggestionBlock(text) : false;
    },
  },
});

DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.tagName === 'A' && node.hasAttribute('href')) {
    node.setAttribute('target', '_blank');
    node.setAttribute('rel', 'noopener noreferrer');
  }
});

export function renderMarkdown(source: string): string {
  return DOMPurify.sanitize(markdown.parse(source, { async: false }), { ADD_ATTR: ['target'] });
}
