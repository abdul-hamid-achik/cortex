/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

const MAX_CAPTURE_BYTES = 4 * 1024 * 1024;

/**
 * Runs the cortex binary and normalizes every result into one shape the
 * renderer can render without knowing whether the call succeeded, failed a
 * kernel gate, or could not find the binary at all.
 */
export function createCortex({ settings, clock = Date.now }) {
  /** Where the binary comes from, in precedence order. */
  function candidates(workspace) {
    const out = [];
    const configured = settings?.all?.().binaryPath || '';
    if (configured) out.push({ path: configured, source: 'settings' });
    if (workspace) out.push({ path: path.join(workspace, 'bin', 'cortex'), source: 'workspace bin/' });
    out.push({ path: path.join(process.cwd(), 'bin', 'cortex'), source: 'cwd bin/' });
    out.push({ path: 'cortex', source: 'PATH' });
    return out;
  }

  function resolveBinary(workspace) {
    for (const candidate of candidates(workspace)) {
      if (candidate.path === 'cortex') return { ...candidate, exists: true };
      const absolute = path.resolve(candidate.path);
      try {
        const stat = fs.statSync(absolute);
        if (stat.isFile()) return { path: absolute, source: candidate.source, exists: true };
      } catch {
        /* try the next candidate */
      }
    }
    return { path: 'cortex', source: 'PATH', exists: false };
  }

  function approvalEnv() {
    const approvals = settings?.all?.().approvals ?? {};
    const env = { ...process.env, NO_COLOR: '1', TERM: 'dumb' };
    // The desktop app is a trusted launcher in the same sense the shell is:
    // these variables are only ever set from an explicit user toggle.
    if (approvals.commands) env.CORTEX_APPROVE_COMMANDS = '1';
    else delete env.CORTEX_APPROVE_COMMANDS;
    if (approvals.remoteRecall) env.CORTEX_APPROVE_REMOTE_RECALL = '1';
    else delete env.CORTEX_APPROVE_REMOTE_RECALL;
    if (approvals.trajectory) env.CORTEX_APPROVE_TRAJECTORY = '1';
    else delete env.CORTEX_APPROVE_TRAJECTORY;
    return env;
  }

  /**
   * @param {string[]} argv cortex argv WITHOUT the binary and without -C/--json
   * @param {object} opts {workspace, json, timeoutMs, approvalsEnv, onChunk, signal}
   */
  function exec(argv, opts = {}) {
    const workspace = opts.workspace || settings?.all?.().workspace || process.cwd();
    const binary = resolveBinary(workspace);
    // -C must precede the subcommand: cobra rejects `cortex --version -C <dir>`
    // as an unknown command, and `cortex -C <dir> <cmd>` is the documented form.
    const fullArgv = [];
    if (workspace) fullArgv.push('-C', workspace);
    fullArgv.push(...argv);
    if (opts.json !== false) fullArgv.push('--json');

    const started = clock();
    return new Promise((resolve) => {
      if (!binary.exists) {
        resolve({
          ok: false,
          stage: 'spawn',
          argv,
          fullArgv,
          binary,
          workspace,
          exitCode: null,
          durationMs: 0,
          json: null,
          stdout: '',
          stderr: `cortex binary not found (looked for: ${candidates(workspace).map((c) => c.path).join(', ')})`,
          error: 'cortex binary not found',
        });
        return;
      }

      let child;
      try {
        child = spawn(binary.path, fullArgv, {
          cwd: workspace || process.cwd(),
          env: opts.env ? { ...approvalEnv(), ...opts.env } : approvalEnv(),
          windowsHide: true,
        });
      } catch (err) {
        resolve({
          ok: false,
          stage: 'spawn',
          argv,
          fullArgv,
          binary,
          workspace,
          exitCode: null,
          durationMs: clock() - started,
          json: null,
          stdout: '',
          stderr: String(err?.message ?? err),
          error: String(err?.message ?? err),
        });
        return;
      }

      let stdout = '';
      let stderr = '';
      let stdoutTruncated = false;
      let stderrTruncated = false;
      let settled = false;

      const append = (which, chunk) => {
        const text = chunk.toString('utf8');
        if (opts.onChunk) opts.onChunk(which, text);
        if (which === 'stdout') {
          if (stdout.length < MAX_CAPTURE_BYTES) stdout += text;
          else stdoutTruncated = true;
        } else if (stderr.length < MAX_CAPTURE_BYTES) stderr += text;
        else stderrTruncated = true;
      };

      child.stdout.on('data', (c) => append('stdout', c));
      child.stderr.on('data', (c) => append('stderr', c));

      const timeoutMs = opts.timeoutMs ?? settings?.all?.().timeouts?.defaultMs ?? 120_000;
      const timer = setTimeout(() => {
        child.kill('SIGTERM');
        setTimeout(() => {
          if (!child.killed) child.kill('SIGKILL');
        }, 3000).unref?.();
      }, timeoutMs);
      timer.unref?.();

      const abort = () => child.kill('SIGTERM');
      if (opts.signal) {
        if (opts.signal.aborted) abort();
        else opts.signal.addEventListener('abort', abort, { once: true });
      }

      const finish = (result) => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        resolve(result);
      };

      child.on('error', (err) => {
        finish({
          ok: false,
          stage: 'spawn',
          argv,
          fullArgv,
          binary,
          workspace,
          exitCode: null,
          durationMs: clock() - started,
          json: null,
          stdout,
          stderr: stderr || String(err?.message ?? err),
          error: String(err?.message ?? err),
        });
      });

      child.on('close', (exitCode, signal) => {
        const trimmedStdout = stdout;
        let parsed = null;
        let parseError = null;
        if (opts.json !== false && trimmedStdout.trim()) {
          try {
            parsed = JSON.parse(trimmedStdout);
          } catch (err) {
            parseError = String(err?.message ?? err);
          }
        }
        const envelope = parsed && typeof parsed === 'object' ? parsed : null;
        const kernelOk = envelope ? envelope.ok !== false : true;
        const timedOut = signal === 'SIGTERM' || signal === 'SIGKILL';
        const errorText =
          envelope?.error ||
          (parseError ? `invalid JSON from cortex: ${parseError}` : '') ||
          (exitCode === 0 ? '' : stderr.trim() || trimmedStdout.trim() || `cortex exited with code ${exitCode}`) ||
          (timedOut ? `cortex timed out after ${timeoutMs}ms` : '');

        finish({
          ok: exitCode === 0 && kernelOk && !parseError && !timedOut,
          stage: timedOut ? 'timeout' : exitCode === 0 ? 'ok' : 'exit',
          argv,
          fullArgv,
          binary,
          workspace,
          exitCode,
          signal: signal ?? null,
          durationMs: clock() - started,
          json: parsed,
          parseError,
          stdout: trimmedStdout,
          stderr,
          stdoutTruncated,
          stderrTruncated,
          summary: envelope?.summary ?? '',
          error: timedOut ? `timed out after ${timeoutMs}ms` : errorText,
          envelope,
        });
      });
    });
  }

  /** `cortex --version`, used by the status chip and the smoke test. */
  async function probe(workspace) {
    const binary = resolveBinary(workspace);
    if (!binary.exists) return { ...binary, ok: false, version: '', error: 'binary not found' };
    const result = await exec(['--version'], { workspace, json: false, timeoutMs: 15_000 });
    const text = (result.stdout || '').trim();
    return {
      ...binary,
      ok: result.ok,
      version: text,
      raw: text,
      error: result.ok ? '' : result.error || 'version probe failed',
    };
  }

  return { exec, probe, resolveBinary, candidates };
}

/**
 * Lift the shared result envelope fields the UI renders everywhere:
 * summary, error, facts, nextActions, structured actions.
 */
export function envelopeView(result) {
  const env = result?.envelope ?? result?.json ?? null;
  if (!env || typeof env !== 'object') return null;
  return {
    ok: env.ok !== false,
    taskId: env.taskId ?? '',
    phase: env.phase ?? '',
    summary: env.summary ?? '',
    error: env.error ?? '',
    facts: Array.isArray(env.facts) ? env.facts : [],
    nextActions: Array.isArray(env.nextActions) ? env.nextActions : [],
    actions: Array.isArray(env.actions) ? env.actions : [],
    rawAvailable: env.rawAvailable === true,
  };
}
