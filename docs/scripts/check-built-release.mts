import { readFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import { assertBuiltRelease } from '../.vitepress/release-version.mts'

if (import.meta.main) {
  const expectedVersion = Bun.argv[2]
  if (expectedVersion === undefined || expectedVersion === '') {
    throw new Error('expected release version argument is required')
  }
  const outputPath = resolve(Bun.argv[3] ?? '.vitepress/dist/index.html')
  assertBuiltRelease(await readFile(outputPath, 'utf8'), expectedVersion)
  console.log(`built docs release navigation verified: ${expectedVersion}`)
}
