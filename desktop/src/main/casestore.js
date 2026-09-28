/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import fs from 'node:fs';
import path from 'node:path';

const MAX_TEXT_BYTES = 1024 * 1024;
const MAX_LEDGER_RECORDS = 5000;
const MAX_DIR_ENTRIES = 4096;

/** The files a case directory is documented to contain (docs/case-file.md). */
export const CASE_FILES = [
  { name: 'case.json', kind: 'snapshot', label: 'Case', summary: 'Goal, acceptance criteria, workspace, phase, boundary, required verification, revision, actor, lease.' },
  { name: 'plan.json', kind: 'snapshot', label: 'Plan', summary: 'The planning gate: hypotheses with disproof paths, boundary, uncertainty, required verifiers.' },
  { name: 'hypotheses.json', kind: 'snapshot', label: 'Hypotheses', summary: 'Falsifiable explanations, their status, and disproof paths.' },
  { name: 'verification.json', kind: 'snapshot', label: 'Verification', summary: 'Receipts: which claim, which surface, which verifier, passed / failed / not_run.' },
  { name: 'decisions.json', kind: 'snapshot', label: 'Decisions', summary: 'Bounded human questions, options, consequences, and answers.' },
  { name: 'evidence.jsonl', kind: 'ledger', label: 'Evidence', summary: 'Append-only ledger of claims with provenance, confidence, and sensitivity.' },
  { name: 'commands.jsonl', kind: 'ledger', label: 'Tool calls', summary: 'Non-sensitive audit trail of tool invocations.' },
  { name: 'phases.jsonl', kind: 'ledger', label: 'Phases', summary: 'Phase-transition history.' },
  { name: 'summary.md', kind: 'text', label: 'Summary', summary: 'The readable outcome.' },
  { name: 'findings.json', kind: 'snapshot', label: 'Findings', summary: 'Durable bugs, improvements, features, and questions backed by evidence.' },
  { name: 'coverage.json', kind: 'snapshot', label: 'Coverage', summary: 'Survey ledger: which modules were explored or summarized.' },
  { name: 'workplan.json', kind: 'snapshot', label: 'Workplan', summary: 'Dependency-aware campaign items handed out as child cases.' },
  { name: 'jobs.json', kind: 'snapshot', label: 'Jobs', summary: 'Detached background investigation jobs.' },
  { name: 'checkpoint.json', kind: 'snapshot', label: 'Checkpoint', summary: 'Recovery packet used by cortex resume.' },
  { name: 'notes.jsonl', kind: 'ledger', label: 'Notes', summary: 'Human and agent observations with provenance.' },
];

function safeJoin(root, ...parts) {
  const resolvedRoot = path.resolve(root);
  const target = path.resolve(resolvedRoot, ...parts);
  if (target !== resolvedRoot && !target.startsWith(resolvedRoot + path.sep)) {
    throw new Error(`path escapes the case store: ${target}`);
  }
  return target;
}

function statOr(entry) {
  try {
    const s = fs.statSync(entry);
    return { size: s.size, mtime: s.mtime.toISOString(), isDir: s.isDirectory() };
  } catch {
    return { size: 0, mtime: '', isDir: false };
  }
}

function readDir(dir) {
  try {
    return fs.readdirSync(dir, { withFileTypes: true }).slice(0, MAX_DIR_ENTRIES);
  } catch {
    return [];
  }
}

/**
 * Read-only access to the central case store. The CLI stays authoritative for
 * projections; this exists so the deck can show the actual durable files
 * (ledgers, snapshots, raw/) without inventing a second source of truth.
 */
export function createCaseStore({ getRoots }) {
  const roots = () => getRoots() ?? {};

  function sessionsRoot() {
    return roots().sessionsRoot || '';
  }

  function listRepos({ includeArchive = false } = {}) {
    const out = [];
    const root = sessionsRoot();
    if (!root) return out;
    for (const entry of readDir(root)) {
      if (!entry.isDirectory()) continue;
      const dir = path.join(root, entry.name);
      const sessions = readDir(dir).filter((x) => x.isDirectory());
      out.push({
        slug: entry.name,
        path: dir,
        sessions: sessions.length,
        ...statOr(dir),
      });
    }
    if (includeArchive && roots().archiveRoot) {
      for (const entry of readDir(roots().archiveRoot)) {
        if (!entry.isDirectory()) continue;
        const dir = path.join(roots().archiveRoot, entry.name);
        out.push({ slug: entry.name, path: dir, sessions: readDir(dir).filter((x) => x.isDirectory()).length, archived: true, ...statOr(dir) });
      }
    }
    return out.sort((a, b) => (b.mtime || '').localeCompare(a.mtime || ''));
  }

  function listSessions(repoSlug) {
    const root = sessionsRoot();
    if (!root) return [];
    const dir = safeJoin(root, repoSlug);
    return readDir(dir)
      .filter((x) => x.isDirectory())
      .map((x) => {
        const taskDir = path.join(dir, x.name);
        return { taskId: x.name, repo: repoSlug, path: taskDir, ...statOr(taskDir) };
      })
      .sort((a, b) => (b.mtime || '').localeCompare(a.mtime || ''));
  }

  /** Locate a task directory by id across every repo (and the archive). */
  function locate(taskId, { includeArchive = true } = {}) {
    if (typeof taskId !== 'string' || !/^[\w.-]+$/.test(taskId)) return null;
    const root = sessionsRoot();
    if (!root) return null;
    const trees = [root];
    if (includeArchive && roots().archiveRoot) trees.push(roots().archiveRoot);
    for (const tree of trees) {
      for (const repo of readDir(tree)) {
        if (!repo.isDirectory()) continue;
        const dir = path.join(tree, repo.name, taskId);
        try {
          if (fs.statSync(dir).isDirectory()) {
            return { taskId, repo: repo.name, dir, archived: tree !== root };
          }
        } catch {
          /* keep looking */
        }
      }
    }
    return null;
  }

  function listFiles(taskId) {
    const found = locate(taskId);
    if (!found) return { found: null, files: [] };
    const walk = (dir, prefix, depth) => {
      const out = [];
      for (const entry of readDir(dir)) {
        const rel = prefix ? `${prefix}/${entry.name}` : entry.name;
        const abs = path.join(dir, entry.name);
        if (entry.isDirectory()) {
          out.push({ name: rel, isDir: true, size: 0, mtime: '' });
          if (depth < 2) out.push(...walk(abs, rel, depth + 1));
        } else {
          out.push({ name: rel, isDir: false, ...statOr(abs) });
        }
      }
      return out;
    };
    const files = walk(found.dir, '', 0);
    const known = new Map(CASE_FILES.map((f) => [f.name, f]));
    return {
      found,
      files: files.map((f) => ({ ...f, meta: known.get(f.name) ?? null })),
    };
  }

  function readFileBounded(taskId, name, maxBytes = MAX_TEXT_BYTES) {
    const found = locate(taskId);
    if (!found) return { error: 'task not found in the central store' };
    if (!name || name.includes('..') || path.isAbsolute(name)) return { error: 'invalid file name' };
    const abs = safeJoin(found.dir, name);
    let stat;
    try {
      stat = fs.statSync(abs);
    } catch {
      return { error: `no such file: ${name}` };
    }
    if (stat.isDirectory()) return { error: `${name} is a directory` };
    const limit = Math.min(maxBytes, MAX_TEXT_BYTES);
    const fd = fs.openSync(abs, 'r');
    try {
      const buffer = Buffer.alloc(Math.min(stat.size, limit));
      fs.readSync(fd, buffer, 0, buffer.length, 0);
      const text = buffer.toString('utf8');
      const result = {
        name,
        task: found.taskId,
        repo: found.repo,
        path: abs,
        size: stat.size,
        mtime: stat.mtime.toISOString(),
        truncated: stat.size > limit,
        text,
      };
      if (name.endsWith('.json')) {
        try {
          result.json = JSON.parse(text);
        } catch (err) {
          result.parseError = String(err?.message ?? err);
        }
      }
      if (name.endsWith('.jsonl')) result.records = parseJsonl(text);
      return result;
    } finally {
      fs.closeSync(fd);
    }
  }

  return {
    sessionsRoot,
    listRepos,
    listSessions,
    locate,
    listFiles,
    readFileBounded,
    safeJoin,
  };
}

/** Parse a JSONL ledger, keeping corrupt lines visible instead of dropping them. */
export function parseJsonl(text, limit = MAX_LEDGER_RECORDS) {
  const records = [];
  const corrupt = [];
  const lines = String(text ?? '').split('\n');
  for (let i = 0; i < lines.length && records.length < limit; i += 1) {
    const line = lines[i].trim();
    if (!line) continue;
    try {
      records.push({ line: i + 1, value: JSON.parse(line) });
    } catch (err) {
      corrupt.push({ line: i + 1, error: String(err?.message ?? err), preview: line.slice(0, 200) });
    }
  }
  return { records, corrupt, truncated: lines.length > limit };
}

export { MAX_TEXT_BYTES, MAX_LEDGER_RECORDS };
