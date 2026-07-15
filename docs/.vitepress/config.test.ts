import { describe, expect, test } from 'bun:test'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { releaseNavigation } from './config.mts'
import {
  assertBuiltRelease,
  assertNoHardcodedRelease,
  developmentVersion,
  releaseLink,
  resolveReleaseVersion,
} from './release-version.mts'

describe('documentation release version', () => {
  test('uses the release environment value in the navigation', () => {
    expect(releaseNavigation('v1.2.3')).toEqual({
      text: 'v1.2.3',
      link: 'https://github.com/abdul-hamid-achik/cortex/releases/tag/v1.2.3',
    })
  })

  test('uses a clear local-development fallback', () => {
    expect(resolveReleaseVersion(undefined)).toBe(developmentVersion)
    expect(releaseNavigation(undefined)).toEqual({
      text: developmentVersion,
      link: 'https://github.com/abdul-hamid-achik/cortex/releases',
    })
  })

  test('requires an explicit release for production deployment', () => {
    expect(() => releaseNavigation(undefined, true)).toThrow('required for production deployment')
  })

  test('rejects malformed release versions', () => {
    expect(() => resolveReleaseVersion('1.2.3')).toThrow('v-prefixed semantic version')
    expect(() => resolveReleaseVersion('v1.2')).toThrow('v-prefixed semantic version')
    expect(() => resolveReleaseVersion('v01.2.3')).toThrow('v-prefixed semantic version')
    expect(() => resolveReleaseVersion('v1.2.3-01')).toThrow('v-prefixed semantic version')
  })

  test('release assertion compares the build value with the release tag', async () => {
    const script = fileURLToPath(new URL('../scripts/check-release-version.mts', import.meta.url))
    const matching = Bun.spawn([process.execPath, 'run', script, 'v1.2.3'], {
      env: { ...process.env, VITEPRESS_VERSION: 'v1.2.3' },
      stderr: 'pipe',
      stdout: 'pipe',
    })
    expect(await matching.exited).toBe(0)

    const mismatched = Bun.spawn([process.execPath, 'run', script, 'v1.2.3'], {
      env: { ...process.env, VITEPRESS_VERSION: 'v1.2.4' },
      stderr: 'pipe',
      stdout: 'pipe',
    })
    expect(await mismatched.exited).not.toBe(0)
  })

  test('config has no second hardcoded release version', async () => {
    const source = await readFile(new URL('./config.mts', import.meta.url), 'utf8')
    expect(() => assertNoHardcodedRelease(source)).not.toThrow()
    expect(() => assertNoHardcodedRelease("{ text: 'v9.8.7' }"))
      .toThrow('hardcoded release version')
  })

  test('release links remain derived from the same version', () => {
    expect(releaseLink('v2.0.0-rc.1'))
      .toBe('https://github.com/abdul-hamid-achik/cortex/releases/tag/v2.0.0-rc.1')
  })

  test('built release assertion binds the rendered label to its exact link', () => {
    const source = '<a href="https://github.com/abdul-hamid-achik/cortex/releases/tag/v1.2.3"><span>v1.2.3</span></a>'
    expect(() => assertBuiltRelease(source, 'v1.2.3')).not.toThrow()
    expect(() => assertBuiltRelease(source, 'v1.2.4')).toThrow('no navigation link')
    expect(() => assertBuiltRelease(source.replace('>v1.2.3</span>', '>dev</span>'), 'v1.2.3'))
      .toThrow('navigation label does not match')
  })
})
