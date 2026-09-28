/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Syntax check every JS file the app ships, then run a headless smoke pass in
// Electron: load each view, fail on any renderer console error, and write a
// screenshot per view so the design is reviewable without opening the app.
//
// Usage: node scripts/smoke.mjs [--only view1,view2]

import { spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..');
const electron = path.resolve(root, 'node_modules', '.bin', 'electron');

const args = process.argv.slice(2);
const onlyIndex = args.indexOf('--only');
const only = onlyIndex >= 0 ? args[onlyIndex + 1].split(',') : null;

if (!fs.existsSync(electron)) {
  console.error('electron is not installed — run `npm install` inside desktop/ first');
  process.exit(2);
}

const child = spawn(electron, ['.'], {
  cwd: root,
  stdio: ['ignore', 'pipe', 'pipe'],
  env: { ...process.env, CORTEX_DECK_SMOKE: '1', ...(only ? { CORTEX_DECK_SMOKE_ONLY: only.join(',') } : {}) },
});

let stdout = '';
let stderr = '';
child.stdout.on('data', (chunk) => { stdout += chunk; process.stdout.write(chunk); });
child.stderr.on('data', (chunk) => { stderr += chunk; process.stderr.write(chunk); });

const timeout = setTimeout(() => {
  console.error('smoke run exceeded 6 minutes — killing electron');
  child.kill('SIGKILL');
}, 360_000);

child.on('close', (code) => {
  clearTimeout(timeout);
  const reportPath = path.join(root, 'screenshots', 'smoke-report.json');
  let report = null;
  try { report = JSON.parse(fs.readFileSync(reportPath, 'utf8')); } catch { report = null; }

  const summary = {
    exitCode: code,
    reportFound: Boolean(report),
    rendererReady: report?.rendererReady ?? null,
    views: report?.views?.length ?? 0,
    screenshots: report?.screenshots?.length ?? 0,
    errors: report?.errors ?? [],
    stderrTail: stderr.trim().split('\n').slice(-12),
  };
  console.log('\n── smoke summary ──');
  console.log(JSON.stringify(summary, null, 2));
  process.exit(code === 0 && report?.ok ? 0 : 1);
});
