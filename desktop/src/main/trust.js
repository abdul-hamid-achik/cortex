/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import fs from 'node:fs';
import path from 'node:path';

/** Go module path of the cortex repository itself. */
export const CORTEX_MODULE = 'github.com/abdul-hamid-achik/cortex';

/** Hostnames the deck will hand to the OS browser. Exact match only. */
export const EXTERNAL_HOSTS = Object.freeze(['github.com', 'cortexai.tools']);

/** Variables that grant trust to cortex; only the explicit Settings toggles may set them. */
export const APPROVAL_VARS = Object.freeze([
  'CORTEX_APPROVE_COMMANDS',
  'CORTEX_APPROVE_REMOTE_RECALL',
  'CORTEX_APPROVE_TRAJECTORY',
]);

/**
 * Parse with the WHATWG URL parser and compare components, never the raw
 * string: `https://github.com.evil.example` and `https://github.com@evil.example`
 * both start with an allowed prefix but resolve to a different host.
 * Returns the normalized href, or '' when the URL is not allowed.
 */
export function allowedExternalUrl(value) {
  let url;
  try {
    url = new URL(String(value ?? ''));
  } catch {
    return '';
  }
  if (url.protocol !== 'https:') return '';
  if (url.username || url.password) return '';
  if (url.port) return '';
  if (!EXTERNAL_HOSTS.includes(url.hostname)) return '';
  return url.href;
}

/** A copy of env with every CORTEX_APPROVE_* grant removed. */
export function withoutApprovals(env = process.env) {
  const out = { ...env };
  for (const name of APPROVAL_VARS) delete out[name];
  return out;
}

function realDir(dir) {
  try {
    return fs.realpathSync(dir);
  } catch {
    return '';
  }
}

/** True when dir holds the cortex repository's go.mod (module path checked). */
export function isCortexRepo(dir) {
  if (!dir) return false;
  try {
    const gomod = fs.readFileSync(path.join(dir, 'go.mod'), 'utf8');
    return gomod.match(/^module\s+(\S+)/m)?.[1] === CORTEX_MODULE;
  } catch {
    return false;
  }
}

/** The repository root above appRoot (desktop/), or '' for packaged builds. */
export function cortexRepoRoot(appRoot) {
  const candidate = path.resolve(appRoot, '..');
  return isCortexRepo(candidate) ? realDir(candidate) : '';
}

/**
 * Dev tasks run `task`/`go` from the workspace, so the Taskfile and any go
 * toolchain hooks there are executed. That is only acceptable for the very
 * checkout the deck itself ships from: a go.mod module name alone is spoofable
 * by an untrusted repository, so the workspace must also be that same
 * directory.
 */
export function isDeckRepo(workspace, repoRoot) {
  if (!workspace || !repoRoot) return false;
  const real = realDir(workspace);
  return real !== '' && real === realDir(repoRoot) && isCortexRepo(real);
}
