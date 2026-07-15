import { describe, expect, test } from 'bun:test'
import { readFile } from 'node:fs/promises'
import docsConfig, { releaseNavigation } from './config.mts'

describe('documentation release navigation', () => {
  test('links to GitHub latest without duplicating a version', () => {
    expect(releaseNavigation).toEqual({
      text: 'Latest release',
      link: 'https://github.com/abdul-hamid-achik/cortex/releases/latest',
    })
    expect(docsConfig.themeConfig?.nav).toContainEqual(releaseNavigation)
  })

  test('contains no hardcoded semantic release', async () => {
    const source = await readFile(new URL('./config.mts', import.meta.url), 'utf8')
    expect(source).not.toMatch(/(['"`])v\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?\1/)
  })
})
