# Documentation deployment

The public documentation is an isolated VitePress application rooted entirely in `docs/`. Vercel
must use that directory as the project root; the Go source tree is never part of the website build
context.

## Local development

From the repository root:

```bash
task docsdeps   # frozen install from docs/bun.lock
task docs       # http://127.0.0.1:5173
task docstest   # release-version contract tests
task docsbuild  # production build → docs/.vitepress/dist
```

You can also work directly from `docs/`:

```bash
bun install --frozen-lockfile
bun run dev
bun run build
bun run preview
```

Without `VITEPRESS_VERSION`, local development and builds label the navigation release as `dev`.
Release builds use the Git tag as the only public version source:

```bash
VITEPRESS_VERSION=v1.2.3 bun run check:release-version v1.2.3
VITEPRESS_VERSION=v1.2.3 bun run build
```

`VITEPRESS_VERSION` must be a `v`-prefixed semantic version. The release workflow derives it from
`github.ref_name`, checks that the navigation label and release link match that tag, rejects a
second hardcoded release in the VitePress config, and then builds the site. The same tag-triggered
workflow performs a pinned Vercel CLI prebuild with that value, confirms the immutable output
contains a navigation label bound to the exact tag URL, and deploys that exact output to
production. Release workflows do not overlap. GitHub keeps at most one pending run in a concurrency
group and does not guarantee dispatch order, so publish one release tag at a time and wait for its
workflow before pushing another. Preview and local builds may omit the variable and retain the
explicit `dev` label.

## Vercel project contract

The project configuration lives in [`docs/vercel.json`](https://github.com/abdul-hamid-achik/cortex/blob/main/docs/vercel.json):

- project root: `docs`
- install command: `bun install --frozen-lockfile`
- build command: `bun run build`
- output directory: `.vitepress/dist`
- production domain: `cortexai.tools`

Link the Git repository once from its root using Vercel's repository mapping. This creates
`.vercel/repo.json` at the repository root; do not run a standalone `vercel link` inside `docs/`,
because that local project link applies the remote Root Directory a second time (`docs/docs`).
Automatic Git deployments are disabled in `docs/vercel.json`: only the tag-triggered release
workflow may publish production documentation, so a later `main` push cannot overwrite the release
label with `dev`.

The GitHub repository must provide `HOMEBREW_TAP_TOKEN`, `VERCEL_TOKEN`, `VERCEL_ORG_ID`, and
`VERCEL_PROJECT_ID` as Actions secrets. The workflow checks all four before building or publishing,
so a release cannot silently omit the Homebrew tap update. For an operator-approved manual
recovery, use the same prebuilt sequence from the repository root:

```bash
vercel link --repo --yes --project cortex --scope the-lacanians
VITEPRESS_VERSION=v1.2.3 vercel pull --yes --environment=production
VITEPRESS_VERSION=v1.2.3 vercel build --prod
vercel deploy --prebuilt --prod
```

The generated `.vercel/` directory is local account state and must not be committed. Vercel's Git
integration should likewise be configured with `docs` as its Root Directory.

## Brand assets

The favicon, navigation mark, hero diagram, and social card are original SVG assets under
`docs/public/`. They share the Cortex palette and remain local so the site has no third-party image
or font requests. Motion is CSS-only and respects `prefers-reduced-motion`.

## Release check

Before publishing documentation:

1. Set `VITEPRESS_VERSION` to the release tag and run the release assertion shown above.
2. Run `task docsbuild` with the same environment value.
3. Start `bun run --cwd docs preview` and inspect the home page plus at least one reference page.
4. Check the browser console and error overlay.
5. Push the annotated tag and wait for the release workflow's prebuilt deployment to finish.
6. Confirm `https://cortexai.tools` renders that exact tag.
