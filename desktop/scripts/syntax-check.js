/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Parse-check every JS file the app ships. There is no bundler and no linter
// dependency, so this is the cheap gate that catches a typo before Electron does.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const targets = ['src', 'test', 'scripts'];
const files = [];

const walk = (dir) => {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === 'node_modules' || entry.name.startsWith('.')) continue;
    const abs = path.join(dir, entry.name);
    if (entry.isDirectory()) walk(abs);
    else if (/\.(js|cjs|mjs)$/.test(entry.name)) files.push(abs);
  }
};

for (const target of targets) {
  const dir = path.join(root, target);
  if (fs.existsSync(dir)) walk(dir);
}

let failed = 0;
for (const file of files) {
  const result = spawnSync(process.execPath, ['--check', file], { encoding: 'utf8' });
  if (result.status !== 0) {
    failed += 1;
    console.error(`✖ ${path.relative(root, file)}`);
    console.error((result.stderr || result.stdout || '').trim());
  }
}

console.log(`${failed === 0 ? '✓' : '✖'} syntax-checked ${files.length} files (${failed} failed)`);
process.exit(failed === 0 ? 0 : 1);
