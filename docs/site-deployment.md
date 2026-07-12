# Documentation deployment

The public documentation is an isolated VitePress application rooted entirely in `docs/`. Vercel
must use that directory as the project root; the Go source tree is never part of the website build
context.

## Local development

From the repository root:

```bash
task docsdeps   # frozen install from docs/bun.lock
task docs       # http://127.0.0.1:5173
task docsbuild  # production build → docs/.vitepress/dist
```

You can also work directly from `docs/`:

```bash
bun install --frozen-lockfile
bun run dev
bun run build
bun run preview
```

## Vercel project contract

The project configuration lives in [`docs/vercel.json`](https://github.com/abdul-hamid-achik/cortex/blob/main/docs/vercel.json):

- project root: `docs`
- install command: `bun install --frozen-lockfile`
- build command: `bun run build`
- output directory: `.vitepress/dist`
- production domain: `cortexai.tools`

Link the Git repository once from its root using Vercel's repository mapping, then deploy the mapped
`docs/` project. This creates `.vercel/repo.json` at the repository root; do not run a standalone
`vercel link` inside `docs/`, because that local project link applies the remote Root Directory a
second time (`docs/docs`):

```bash
vercel link --repo --yes --project cortex --scope the-lacanians
vercel deploy --cwd docs --scope the-lacanians
vercel deploy --cwd docs --prod --scope the-lacanians
```

The generated `.vercel/` directory is local account state and must not be committed. Vercel's Git
integration should likewise be configured with `docs` as its Root Directory.

## Brand assets

The favicon, navigation mark, hero diagram, and social card are original SVG assets under
`docs/public/`. They share the Cortex palette and remain local so the site has no third-party image
or font requests. Motion is CSS-only and respects `prefers-reduced-motion`.

## Release check

Before publishing documentation:

1. Run `task docsbuild`.
2. Start `bun run --cwd docs preview` and inspect the home page plus at least one reference page.
3. Check the browser console and error overlay.
4. Deploy a preview and verify its immutable URL.
5. Promote or deploy the same commit to production, then confirm `https://cortexai.tools`.
