import { defineConfig } from 'vitepress'

export const releaseNavigation = {
  text: 'Latest release',
  link: 'https://github.com/abdul-hamid-achik/cortex/releases/latest',
}

export default defineConfig({
  title: 'Cortex',
  titleTemplate: ':title — Cortex',
  description: 'The local-first agent kernel for evidence-guided software engineering.',
  lang: 'en-US',
  cleanUrls: true,
  lastUpdated: true,
  srcDir: '.',
  base: process.env.VITEPRESS_BASE ?? '/',
  sitemap: { hostname: 'https://cortexai.tools' },

  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }],
    ['link', { rel: 'mask-icon', href: '/cortex-mark.svg', color: '#d97757' }],
    ['link', { rel: 'apple-touch-icon', sizes: '180x180', href: '/apple-touch-icon.png' }],
    ['link', { rel: 'manifest', href: '/site.webmanifest' }],
    ['meta', { name: 'theme-color', content: '#141413' }],
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:site_name', content: 'Cortex' }],
    ['meta', { property: 'og:title', content: 'Cortex — evidence-guided agent kernel' }],
    ['meta', { property: 'og:description', content: 'Durable state, bounded changes, and verification for software-engineering agents.' }],
    ['meta', { property: 'og:image', content: 'https://cortexai.tools/og-card.png' }],
    ['meta', { name: 'twitter:card', content: 'summary_large_image' }],
  ],

  themeConfig: {
    logo: { src: '/cortex-mark.svg', alt: 'Cortex' },
    siteTitle: 'Cortex',
    search: { provider: 'local' },
    nav: [
      { text: 'Quick Start', link: '/quick-start' },
      { text: 'Tutorial', link: '/tutorial' },
      { text: 'Concepts', link: '/concepts' },
      { text: 'Studio', link: '/studio' },
      {
        text: 'Reference',
        items: [
          { text: 'CLI', link: '/cli' },
          { text: 'MCP server', link: '/mcp' },
          { text: 'Configuration', link: '/configuration' },
          { text: 'Case file', link: '/case-file' },
          { text: 'Contract fixtures', link: '/contracts' },
          { text: 'Evaluation', link: '/evaluation' },
          { text: 'FAQ', link: '/faq' },
        ],
      },
      releaseNavigation,
    ],
    sidebar: [
      {
        text: 'Get Started',
        items: [
          { text: 'Overview', link: '/' },
          { text: 'Quick Start', link: '/quick-start' },
          { text: 'Tutorial', link: '/tutorial' },
          { text: 'Concepts', link: '/concepts' },
          { text: 'Configuration', link: '/configuration' },
          { text: 'FAQ', link: '/faq' },
        ],
      },
      {
        text: 'Surfaces',
        items: [
          { text: 'CLI', link: '/cli' },
          { text: 'MCP server', link: '/mcp' },
          { text: 'Studio for operators', link: '/studio' },
        ],
      },
      {
        text: 'Reference',
        items: [
          { text: 'For agents', link: '/agents' },
          { text: 'Adapters & ecosystem', link: '/adapters' },
          { text: 'The case file', link: '/case-file' },
          { text: 'Public contracts', link: '/contracts' },
          { text: 'Empirical evaluation', link: '/evaluation' },
          { text: 'Documentation deployment', link: '/site-deployment' },
        ],
      },
    ],
    outline: { level: [2, 3], label: 'On this page' },
    docFooter: { prev: 'Previous', next: 'Next' },
    editLink: {
      pattern: 'https://github.com/abdul-hamid-achik/cortex/edit/main/docs/:path',
      text: 'Improve this page on GitHub',
    },
    socialLinks: [
      { icon: 'github', link: 'https://github.com/abdul-hamid-achik/cortex' },
    ],
    footer: {
      message: 'Local-first. Evidence-guided. Built for agents and the people supervising them.',
      copyright: 'MIT Licensed © 2026 Abdul Hamid Achik',
    },
  },
})
