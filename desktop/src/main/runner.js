/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { spawn } from 'node:child_process';
import { DEV_TASKS } from './registry.js';

const MAX_LINES = 4000;

/**
 * Runs the project's own curated developer tasks (Taskfile entries and a couple
 * of direct go commands) with live streaming output.
 *
 * Only ids present in DEV_TASKS may run — the deck never executes a free-form
 * shell string, so a compromised renderer cannot become a shell.
 */
export function createRunner({ getWorkspace, emit }) {
  const runs = new Map();
  let seq = 1;
  const byId = new Map(DEV_TASKS.map((t) => [t.id, t]));

  function pushLine(run, stream, text) {
    run.lines.push({ stream, text, at: Date.now() });
    if (run.lines.length > MAX_LINES) run.lines.splice(0, run.lines.length - MAX_LINES);
    emit({ type: 'run-output', runId: run.id, stream, text, at: run.lines.at(-1).at });
  }

  function start(taskId) {
    const task = byId.get(taskId);
    if (!task) return { error: `unknown task: ${taskId}` };
    const workspace = getWorkspace();
    if (!workspace) return { error: 'no workspace selected' };

    const run = {
      id: `run_${seq++}`,
      taskId,
      label: task.label,
      cmd: task.cmd,
      args: task.args,
      workspace,
      state: 'running',
      startedAt: new Date().toISOString(),
      finishedAt: null,
      exitCode: null,
      lines: [],
    };
    runs.set(run.id, run);
    emit({ type: 'run-start', run });

    let child;
    try {
      child = spawn(task.cmd, task.args, {
        cwd: workspace,
        windowsHide: true,
        env: { ...process.env, NO_COLOR: '1', FORCE_COLOR: '0' },
      });
    } catch (err) {
      run.state = 'error';
      run.finishedAt = new Date().toISOString();
      pushLine(run, 'stderr', String(err?.message ?? err));
      emit({ type: 'run-end', run });
      return { run: publicRun(run) };
    }

    run.pid = child.pid;
    let pending = '';
    child.stdout.setEncoding('utf8');
    child.stderr.setEncoding('utf8');
    const onData = (stream) => (chunk) => {
      pending += chunk;
      let index = pending.indexOf('\n');
      while (index >= 0) {
        pushLine(run, stream, pending.slice(0, index));
        pending = pending.slice(index + 1);
        index = pending.indexOf('\n');
      }
    };
    child.stdout.on('data', onData('stdout'));
    child.stderr.on('data', onData('stderr'));
    child.on('error', (err) => pushLine(run, 'stderr', String(err?.message ?? err)));
    child.on('close', (code, signal) => {
      if (pending) pushLine(run, 'stdout', pending);
      pending = '';
      run.state = code === 0 ? 'passed' : 'failed';
      run.exitCode = code;
      run.signal = signal ?? null;
      run.finishedAt = new Date().toISOString();
      run.child = null;
      emit({ type: 'run-end', run: publicRun(run) });
    });

    run.child = child;
    return { run: publicRun(run) };
  }

  function kill(runId) {
    const run = runs.get(runId);
    if (!run) return { error: 'unknown run' };
    if (!run.child) return { ok: true, state: run.state };
    run.child.kill('SIGTERM');
    return { ok: true, state: 'terminating' };
  }

  function publicRun(run) {
    const { child, lines, ...rest } = run;
    return { ...rest, lineCount: lines.length };
  }

  return {
    start,
    kill,
    list: () => [...runs.values()].map(publicRun),
    output: (runId, tail = 1000) => {
      const run = runs.get(runId);
      if (!run) return { error: 'unknown run' };
      return { runId, lines: run.lines.slice(-tail), state: run.state, exitCode: run.exitCode };
    },
    clear: () => {
      for (const run of runs.values()) if (!run.child) runs.delete(run.id);
      return { cleared: true };
    },
    tasks: () => DEV_TASKS,
  };
}
