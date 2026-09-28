/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Renders the repository's brand mark (docs/public/cortex-mark.svg — the same
// artwork the docs site and favicon use) into every size the desktop app and
// its packager need. Run: npx electron scripts/make-icon.mjs
//
// Outputs (all under desktop/):
//   build/icon.png            1024² source-of-truth raster
//   build/icon-<size>.png     512/256/128/64/32/16
//   build/icon.icns           macOS icon (iconutil; skipped off-darwin)
//   src/renderer/assets/icon.png   256² copy the shell shows in rail + splash

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { app, BrowserWindow } from 'electron';

const here = path.dirname(fileURLToPath(import.meta.url));
const desktopRoot = path.resolve(here, '..');
const repoRoot = path.resolve(desktopRoot, '..');
const MARK = path.join(repoRoot, 'docs', 'public', 'cortex-mark.svg');

const SIZES = [16, 32, 64, 128, 256, 512, 1024];
// iconutil wants exactly this ladder inside the .iconset bundle.
const ICONSET = [
  ['icon_16x16.png', 16],
  ['icon_16x16@2x.png', 32],
  ['icon_32x32.png', 32],
  ['icon_32x32@2x.png', 64],
  ['icon_128x128.png', 128],
  ['icon_128x128@2x.png', 256],
  ['icon_256x256.png', 256],
  ['icon_256x256@2x.png', 512],
  ['icon_512x512.png', 512],
  ['icon_512x512@2x.png', 1024],
];

async function main() {
  const svg = fs.readFileSync(MARK, 'utf8');
  if (!svg.includes('<svg')) throw new Error(`unexpected mark at ${MARK}`);

  const buildDir = path.join(desktopRoot, 'build');
  const assetDir = path.join(desktopRoot, 'src', 'renderer', 'assets');
  fs.mkdirSync(buildDir, { recursive: true });
  fs.mkdirSync(assetDir, { recursive: true });

  await app.whenReady();
  const win = new BrowserWindow({
    show: false,
    width: 1024,
    height: 1024,
    backgroundColor: '#141413',
    webPreferences: { contextIsolation: true, nodeIntegration: false },
  });

  const html = `<!doctype html><html><head><meta charset="utf-8"><style>
    html,body{margin:0;padding:0;width:100%;height:100%;background:#141413;overflow:hidden}
    svg{display:block;width:100%;height:100%}
  </style></head><body>${svg}</body></html>`;

  await win.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(html)}`);
  await new Promise((resolve) => setTimeout(resolve, 250));
  // A never-shown window captures blank on macOS; showInactive paints without
  // stealing focus, exactly like the smoke harness does.
  win.showInactive();
  await new Promise((resolve) => setTimeout(resolve, 150));

  const captured = await win.webContents.capturePage();
  const master = captured.resize({ width: 1024, height: 1024 });
  win.destroy();

  const written = [];
  for (const size of SIZES) {
    const image = size === 1024 ? master : master.resize({ width: size, height: size });
    const file = path.join(buildDir, size === 1024 ? 'icon.png' : `icon-${size}.png`);
    fs.writeFileSync(file, image.toPNG());
    written.push(file);
  }

  const rendererIcon = path.join(assetDir, 'icon.png');
  fs.writeFileSync(rendererIcon, master.resize({ width: 256, height: 256 }).toPNG());
  written.push(rendererIcon);

  let icns = null;
  if (process.platform === 'darwin') {
    const setDir = path.join(buildDir, 'icon.iconset');
    fs.mkdirSync(setDir, { recursive: true });
    for (const [name, size] of ICONSET) {
      fs.writeFileSync(path.join(setDir, name), master.resize({ width: size, height: size }).toPNG());
    }
    const result = spawnSync('iconutil', ['-c', 'icns', setDir, '-o', path.join(buildDir, 'icon.icns')], { encoding: 'utf8' });
    if (result.status === 0) {
      icns = path.join(buildDir, 'icon.icns');
      written.push(icns);
    } else {
      console.error(`iconutil failed: ${result.stderr || result.stdout}`);
    }
    fs.rmSync(setDir, { recursive: true, force: true });
  }

  console.log(JSON.stringify({ ok: true, mark: MARK, written, icns }, null, 2));
  app.exit(0);
}

main().catch((err) => {
  console.error(err);
  app.exit(1);
});
