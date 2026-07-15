# Documentation deployment

The public documentation is an isolated VitePress application rooted entirely in `docs/`. Vercel
must use that directory as the project root; the Go source tree is never part of the website build
context.

## Local development

From the repository root:

```bash
task docsdeps   # frozen install from docs/bun.lock
task docs       # http://127.0.0.1:5173
task docstest   # documentation configuration tests
task docsbuild  # optimized build → docs/.vitepress/dist
```

You can also work directly from `docs/`:

```bash
bun install --frozen-lockfile
bun run dev
bun run build
bun run preview
```

The navigation does not embed a release number. `Latest release` links to GitHub's stable
`/releases/latest` redirect, so documentation builds never need a tag, release file, or deployment
environment variable. Release workflows do not overlap. Publish one release tag at a time and wait
for its workflow before publishing another.

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

Vercel Git Integration is the documentation deployment authority. Pushes to the configured
production branch build and deploy automatically. `docs/vercel.json` does not disable Git
deployments. The GitHub release workflow neither calls Vercel nor needs Vercel credentials, so a
documentation-provider outage cannot block the GitHub release or Homebrew update.

The only manually configured GitHub Actions secret is `HOMEBREW_TAP_TOKEN`; GitHub supplies the
workflow's repository-scoped `GITHUB_TOKEN`. The workflow checks the tap token before building or
publishing, so a release cannot silently omit the Homebrew tap update.

The generated `.vercel/` directory is local account state and must not be committed. Vercel's Git
integration should likewise be configured with `docs` as its Root Directory.

## Brand assets

The favicon, navigation mark, hero diagram, and social card are original SVG assets under
`docs/public/`. They share the Cortex palette and remain local so the site has no third-party image
or font requests. Motion is CSS-only and respects `prefers-reduced-motion`.

## Release check

Before publishing documentation:

1. Run `task docstest` and `task docsbuild`.
2. Start `bun run --cwd docs preview` and inspect the home page plus at least one reference page.
3. Check the browser console and error overlay.
4. Push `main`; Vercel builds and deploys it through Git Integration.
5. Confirm `https://cortexai.tools` renders the update and its `Latest release` link resolves.
