export const developmentVersion = 'dev'

const releaseVersionPattern = new RegExp(
  '^v(0|[1-9]\\d*)\\.(0|[1-9]\\d*)\\.(0|[1-9]\\d*)' +
  '(?:-([0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*))?' +
  '(?:\\+[0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*)?$',
)

export function resolveReleaseVersion(value: string | undefined, required = false): string {
  if (value === undefined || value.trim() === '') {
    if (required) {
      throw new Error('VITEPRESS_VERSION is required for production deployment')
    }
    return developmentVersion
  }

  const match = releaseVersionPattern.exec(value)
  const prerelease = match?.[4]
  const hasInvalidNumericPrerelease = prerelease
    ?.split('.')
    .some((identifier) => (
      /^\d+$/.test(identifier) && identifier.length > 1 && identifier.startsWith('0')
    ))

  if (match === null || hasInvalidNumericPrerelease) {
    throw new Error(
      `VITEPRESS_VERSION must be a v-prefixed semantic version, got ${JSON.stringify(value)}`,
    )
  }

  return value
}

export function releaseLink(version: string): string {
  const releases = 'https://github.com/abdul-hamid-achik/cortex/releases'
  return version === developmentVersion
    ? releases
    : `${releases}/tag/${encodeURIComponent(version)}`
}

export function assertNoHardcodedRelease(source: string): void {
  const hardcodedRelease = /(['"`])v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?\1/
  if (hardcodedRelease.test(source)) {
    throw new Error('docs config contains a hardcoded release version')
  }
}

export function assertBuiltRelease(source: string, expectedVersion: string): void {
  const version = resolveReleaseVersion(expectedVersion, true)
  const link = releaseLink(version)
  const linkToken = `href="${link}"`
  const linkStart = source.indexOf(linkToken)
  if (linkStart < 0) {
    throw new Error(`built documentation has no navigation link for ${JSON.stringify(version)}`)
  }
  const linkEnd = source.indexOf('</a>', linkStart)
  const anchor = linkEnd < 0 ? '' : source.slice(linkStart, linkEnd)
  if (!anchor.includes(`>${version}</span>`)) {
    throw new Error(`built documentation navigation label does not match ${JSON.stringify(version)}`)
  }
}
