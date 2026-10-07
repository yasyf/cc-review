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

const ALERT = /^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\][ \t]*\n?/i;

const HTML_COMMENT = /<!--[\s\S]*?(?:-->|$)/g;

const markdown: Marked = new Marked({
  gfm: true,
  breaks: true,
  renderer: {
    code({ text, lang }) {
      return lang === 'suggestion' ? suggestionBlock(text) : false;
    },
    html({ text }) {
      return text.replace(HTML_COMMENT, '');
    },
    blockquote({ text }) {
      const match = ALERT.exec(text);
      if (!match) return false;
      const kind = match[1].toLowerCase();
      const body = this.parser.parse(markdown.lexer(text.slice(match[0].length)));
      const title = kind.charAt(0).toUpperCase() + kind.slice(1);
      return `<div class="md-alert md-alert-${kind}"><p class="md-alert-title">${title}</p>${body}</div>`;
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
