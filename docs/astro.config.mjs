// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

export default defineConfig({
  site: 'https://yasyf.github.io',
  base: '/cc-review',
  integrations: [
    starlight({
      title: 'cc-review',
      description:
        'Review the code Claude just wrote in a PR-like web UI before it lands.',
      social: [
        { icon: 'github', label: 'GitHub', href: 'https://github.com/yasyf/cc-review' },
      ],
      editLink: { baseUrl: 'https://github.com/yasyf/cc-review/edit/main/docs/' },
      sidebar: [
        { label: 'Getting started', slug: 'getting-started' },
        { label: 'How a review works', slug: 'how-a-review-works' },
        { label: 'CLI reference', slug: 'cli-reference' },
        { label: 'Internals', slug: 'internals' },
      ],
    }),
  ],
});
