import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'Cortex',
  description: 'An evidence-guided agent kernel for software-engineering agents.',
  lang: 'en-US',
  cleanUrls: true,
  lastUpdated: true,
  srcDir: '.',
  base: process.env.VITEPRESS_BASE ?? '/',

  head: [
    ['meta', { name: 'description', content: 'Cortex documentation site.' }],
  ],

  themeConfig: {
    siteTitle: 'Cortex',
    search: { provider: 'local' },
    nav: [
      { text: 'Quick Start', link: '/quick-start' },
      { text: 'Tutorial', link: '/tutorial' },
      { text: 'Concepts', link: '/concepts' },
      { text: 'CLI', link: '/cli' },
      { text: 'MCP', link: '/mcp' },
      { text: 'FAQ', link: '/faq' },
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
        ],
      },
      {
        text: 'Reference',
        items: [
          { text: 'For agents', link: '/agents' },
          { text: 'Adapters & ecosystem', link: '/adapters' },
          { text: 'The case file', link: '/case-file' },
        ],
      },
    ],
    socialLinks: [
      { icon: 'github', link: 'https://github.com/abdul-hamid-achik/cortex' },
    ],
    footer: {
      message: 'An evidence-guided agent kernel.',
      copyright: 'MIT Licensed © Abdul Hamid Achik',
    },
  },
})
