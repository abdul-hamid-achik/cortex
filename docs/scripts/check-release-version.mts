import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import docsConfig from '../.vitepress/config.mts'
import {
  assertNoHardcodedRelease,
  releaseLink,
  resolveReleaseVersion,
} from '../.vitepress/release-version.mts'

export async function checkReleaseVersion(
  expectedVersion: string | undefined,
  configuredVersion: string | undefined,
): Promise<void> {
  if (expectedVersion === undefined || expectedVersion === '') {
    throw new Error('expected release version argument is required')
  }

  const expected = resolveReleaseVersion(expectedVersion)
  const configured = resolveReleaseVersion(configuredVersion)
  if (configured !== expected) {
    throw new Error(
      `VITEPRESS_VERSION ${JSON.stringify(configured)} does not match release ${JSON.stringify(expected)}`,
    )
  }

  const releaseItem = docsConfig.themeConfig?.nav?.find(
    (item) => 'text' in item && item.text === expected,
  )
  if (
    releaseItem === undefined ||
    !('link' in releaseItem) ||
    releaseItem.link !== releaseLink(expected)
  ) {
    throw new Error(`docs navigation does not point to release ${JSON.stringify(expected)}`)
  }

  const configPath = fileURLToPath(new URL('../.vitepress/config.mts', import.meta.url))
  assertNoHardcodedRelease(await readFile(configPath, 'utf8'))
}

if (import.meta.main) {
  await checkReleaseVersion(Bun.argv[2], process.env.VITEPRESS_VERSION)
  console.log(`docs release version verified: ${process.env.VITEPRESS_VERSION}`)
}
