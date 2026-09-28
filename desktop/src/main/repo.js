/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

const MAX_READ_BYTES = 512 * 1024;
const MAX_ENTRIES = 2000;

function run(cmd, args, cwd, timeoutMs = 20_000) {
  return new Promise((resolve) => {
    let out = '';
    let err = '';
    let child;
    try {
      child = spawn(cmd, args, { cwd, windowsHide: true, env: { ...process.env, NO_COLOR: '1' } });
    } catch (e) {
      resolve({ ok: false, stdout: '', stderr: String(e?.message ?? e) });
      return;
    }
    const timer = setTimeout(() => child.kill('SIGKILL'), timeoutMs);
    child.stdout.on('data', (c) => { out += c; });
    child.stderr.on('data', (c) => { err += c; });
    child.on('error', (e) => { clearTimeout(timer); resolve({ ok: false, stdout: out, stderr: String(e?.message ?? e) }); });
    child.on('close', (code) => { clearTimeout(timer); resolve({ ok: code === 0, code, stdout: out, stderr: err }); });
  });
}

/**
 * Read-only access to the workspace the deck is pointed at: git identity, the
 * documentation/spec/contract corpora, and bounded file reads. Every path is
 * confined to the workspace so the renderer can never reach arbitrary files.
 */
export function createRepo({ getWorkspace }) {
  const ws = () => getWorkspace() || process.cwd();

  function resolve(rel) {
    const root = path.resolve(ws());
    const target = path.resolve(root, rel ?? '');
    if (target !== root && !target.startsWith(root + path.sep)) throw new Error(`path escapes the workspace: ${rel}`);
    return target;
  }

  async function gitInfo() {
    const root = ws();
    const branch = await run('git', ['rev-parse', '--abbrev-ref', 'HEAD'], root);
    if (!branch.ok) return { ok: false, error: branch.stderr.trim() || 'not a git repository' };
    const head = await run('git', ['rev-parse', '--short', 'HEAD'], root);
    const status = await run('git', ['status', '--porcelain'], root);
    const log = await run('git', ['log', '-n', '8', '--pretty=%h%x1f%s%x1f%an%x1f%aI'], root);
    const remotes = await run('git', ['remote', '-v'], root);
    const changes = status.stdout.split('\n').filter(Boolean);
    return {
      ok: true,
      root,
      branch: branch.stdout.trim(),
      head: head.stdout.trim(),
      dirty: changes.length,
      changes: changes.slice(0, 200).map((line) => ({ code: line.slice(0, 2).trim(), path: line.slice(3) })),
      commits: log.stdout.split('\n').filter(Boolean).map((line) => {
        const [hash, subject, author, date] = line.split('\x1f');
        return { hash, subject, author, date };
      }),
      remotes: remotes.stdout.split('\n').filter(Boolean),
    };
  }

  function readText(rel, maxBytes = MAX_READ_BYTES) {
    const abs = resolve(rel);
    let stat;
    try {
      stat = fs.statSync(abs);
    } catch {
      return { error: `no such file: ${rel}` };
    }
    if (stat.isDirectory()) return { error: `${rel} is a directory` };
    const limit = Math.min(maxBytes, MAX_READ_BYTES);
    const fd = fs.openSync(abs, 'r');
    try {
      const buffer = Buffer.alloc(Math.min(stat.size, limit));
      fs.readSync(fd, buffer, 0, buffer.length, 0);
      return {
        path: rel,
        absPath: abs,
        size: stat.size,
        mtime: stat.mtime.toISOString(),
        truncated: stat.size > limit,
        text: buffer.toString('utf8'),
      };
    } finally {
      fs.closeSync(fd);
    }
  }

  function listDir(rel = '', { depth = 1 } = {}) {
    const walk = (dir, prefix, level) => {
      const out = [];
      let entries;
      try {
        entries = fs.readdirSync(dir, { withFileTypes: true }).slice(0, MAX_ENTRIES);
      } catch {
        return out;
      }
      for (const entry of entries) {
        if (entry.name === 'node_modules' || entry.name === '.git') continue;
        const relName = prefix ? `${prefix}/${entry.name}` : entry.name;
        const abs = path.join(dir, entry.name);
        if (entry.isDirectory()) {
          out.push({ path: relName, isDir: true, size: 0 });
          if (level < depth) out.push(...walk(abs, relName, level + 1));
        } else {
          let size = 0;
          try { size = fs.statSync(abs).size; } catch { /* ignore */ }
          out.push({ path: relName, isDir: false, size });
        }
      }
      return out;
    };
    return walk(resolve(''), '', 0);
  }

  function collect(patternDirs, extensions) {
    const out = [];
    for (const dir of patternDirs) {
      let entries;
      try {
        entries = fs.readdirSync(resolve(dir), { withFileTypes: true });
      } catch {
        continue;
      }
      for (const entry of entries) {
        const ext = path.extname(entry.name);
        if (entry.isFile() && extensions.includes(ext)) {
          out.push({ path: `${dir}/${entry.name}`, name: entry.name, size: fs.statSync(resolve(`${dir}/${entry.name}`)).size });
        } else if (entry.isDirectory() && entry.name !== 'node_modules' && entry.name !== '.vitepress' && entry.name !== 'public') {
          out.push(...collect([`${dir}/${entry.name}`], extensions));
        }
      }
    }
    return out.sort((a, b) => a.path.localeCompare(b.path));
  }

  const docs = () => collect(['docs'], ['.md']);
  const specs = () => collect(['specs'], ['.yml', '.yaml']);
  const contracts = () => collect(['contracts'], ['.json']);
  const evaluations = () => collect(['evaluations'], ['.yml', '.yaml', '.json', '.md']);

  /** Extract the Taskfile task list without a YAML dependency. */
  function taskfileTasks() {
    const text = (() => {
      try {
        return fs.readFileSync(resolve('Taskfile.yml'), 'utf8');
      } catch {
        return '';
      }
    })();
    if (!text) return [];
    const lines = text.split('\n');
    const out = [];
    let inTasks = false;
    let current = null;
    for (const line of lines) {
      if (/^tasks:\s*$/.test(line)) { inTasks = true; continue; }
      if (!inTasks) continue;
      if (/^\S/.test(line)) break;
      const taskMatch = line.match(/^ {2}([A-Za-z0-9_.:-]+):\s*$/);
      if (taskMatch) {
        current = { name: taskMatch[1], desc: '', deps: [], cmds: 0 };
        out.push(current);
        continue;
      }
      if (!current) continue;
      const desc = line.match(/^\s+desc:\s*(.+?)\s*$/);
      if (desc) { current.desc = desc[1].replace(/^["']|["']$/g, ''); continue; }
      if (/^\s+-\s/.test(line)) current.cmds += 1;
    }
    return out;
  }

  function projectFacts() {
    const facts = {};
    try {
      const gomod = fs.readFileSync(resolve('go.mod'), 'utf8');
      facts.module = gomod.match(/^module\s+(\S+)/m)?.[1] ?? '';
      facts.goVersion = gomod.match(/^go\s+(\S+)/m)?.[1] ?? '';
      facts.dependencies = gomod.split('\n').filter((l) => /^\t\S+ v/.test(l)).length;
    } catch { /* no go.mod */ }
    try {
      facts.toolVersions = fs.readFileSync(resolve('.tool-versions'), 'utf8').trim();
    } catch { /* optional */ }
    facts.tasks = taskfileTasks();
    try {
      facts.version = JSON.parse(fs.readFileSync(resolve('desktop/package.json'), 'utf8')).version;
    } catch { /* optional */ }
    return facts;
  }

  /** Count Go source and test lines per top-level package — cheap codebase overview. */
  function codeStats() {
    const roots = ['cmd', 'internal'];
    const out = [];
    const walk = (dir, prefix) => {
      let entries;
      try { entries = fs.readdirSync(dir, { withFileTypes: true }); } catch { return; }
      let goFiles = 0;
      let testFiles = 0;
      let lines = 0;
      for (const entry of entries) {
        const abs = path.join(dir, entry.name);
        if (entry.isDirectory()) { walk(abs, `${prefix}/${entry.name}`); continue; }
        if (!entry.name.endsWith('.go')) continue;
        const isTest = entry.name.endsWith('_test.go');
        if (isTest) testFiles += 1; else goFiles += 1;
        try { lines += fs.readFileSync(abs, 'utf8').split('\n').length; } catch { /* ignore */ }
      }
      if (goFiles || testFiles) out.push({ package: prefix.replace(/^\//, ''), goFiles, testFiles, lines });
    };
    for (const root of roots) walk(resolve(root), `/${root}`);
    return out.sort((a, b) => b.lines - a.lines);
  }

  return { workspace: ws, resolve, gitInfo, readText, listDir, docs, specs, contracts, evaluations, taskfileTasks, projectFacts, codeStats };
}
