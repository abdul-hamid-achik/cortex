/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { CORTEX_MODULE, allowedExternalUrl, cortexRepoRoot, isCortexRepo, isDeckRepo, withoutApprovals } from '../src/main/trust.js';
import { createRunner } from '../src/main/runner.js';

const tempDir = () => fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), 'deck-trust-')));

function fakeRepo(moduleName = CORTEX_MODULE) {
  const dir = tempDir();
  fs.writeFileSync(path.join(dir, 'go.mod'), `module ${moduleName}\n\ngo 1.25.5\n`);
  return dir;
}

test('openExternal accepts exact github.com and cortexai.tools https URLs', () => {
  assert.equal(allowedExternalUrl('https://github.com/abdul-hamid-achik/cortex'), 'https://github.com/abdul-hamid-achik/cortex');
  assert.equal(allowedExternalUrl('https://github.com'), 'https://github.com/');
  assert.equal(allowedExternalUrl('https://cortexai.tools/docs?x=1#top'), 'https://cortexai.tools/docs?x=1#top');
  assert.equal(allowedExternalUrl('HTTPS://GitHub.com/x'), 'https://github.com/x');
});

test('openExternal rejects evil-suffix hosts that merely start with an allowed name', () => {
  assert.equal(allowedExternalUrl('https://github.com.evil.example/x'), '');
  assert.equal(allowedExternalUrl('https://cortexai.tools.evil.example/'), '');
  assert.equal(allowedExternalUrl('https://github.comevil.example/'), '');
  assert.equal(allowedExternalUrl('https://notgithub.com/'), '');
  assert.equal(allowedExternalUrl('https://evil.example/github.com'), '');
  assert.equal(allowedExternalUrl('https://evil.example/?u=https://github.com/'), '');
});

test('openExternal rejects userinfo tricks', () => {
  assert.equal(allowedExternalUrl('https://github.com@evil.example/'), '');
  assert.equal(allowedExternalUrl('https://github.com:pw@evil.example/'), '');
  assert.equal(allowedExternalUrl('https://user@github.com/'), '');
  assert.equal(allowedExternalUrl('https://user:pw@cortexai.tools/'), '');
  assert.equal(allowedExternalUrl('https://evil.example\\@github.com/'), '');
});

test('openExternal rejects non-https schemes, ports, and junk', () => {
  assert.equal(allowedExternalUrl('http://github.com/x'), '');
  assert.equal(allowedExternalUrl('file:///etc/passwd'), '');
  assert.equal(allowedExternalUrl('javascript:alert(1)'), '');
  assert.equal(allowedExternalUrl('ftp://github.com/'), '');
  assert.equal(allowedExternalUrl('https://github.com:8443/x'), '');
  assert.equal(allowedExternalUrl('github.com/x'), '');
  assert.equal(allowedExternalUrl(''), '');
  assert.equal(allowedExternalUrl(undefined), '');
  assert.equal(allowedExternalUrl(null), '');
  assert.equal(allowedExternalUrl({}), '');
});

test('withoutApprovals removes every CORTEX_APPROVE_* grant and keeps the rest', () => {
  const env = withoutApprovals({
    PATH: '/bin',
    CORTEX_APPROVE_COMMANDS: '1',
    CORTEX_APPROVE_REMOTE_RECALL: '1',
    CORTEX_APPROVE_TRAJECTORY: '1',
  });
  assert.deepEqual(env, { PATH: '/bin' });
});

test('isCortexRepo checks the go.mod module path, not just its existence', () => {
  assert.equal(isCortexRepo(fakeRepo()), true);
  assert.equal(isCortexRepo(fakeRepo('example.com/other')), false);
  assert.equal(isCortexRepo(tempDir()), false);
  assert.equal(isCortexRepo(''), false);
});

test('cortexRepoRoot resolves the parent of desktop/ only when it is the cortex module', () => {
  const repo = fakeRepo();
  fs.mkdirSync(path.join(repo, 'desktop'));
  assert.equal(cortexRepoRoot(path.join(repo, 'desktop')), repo);

  const foreign = fakeRepo('example.com/other');
  fs.mkdirSync(path.join(foreign, 'desktop'));
  assert.equal(cortexRepoRoot(path.join(foreign, 'desktop')), '');
});

test('isDeckRepo requires the workspace to be the deck repo itself, not a go.mod lookalike', () => {
  const repo = fakeRepo();
  const lookalike = fakeRepo(); // same module path, different directory
  assert.equal(isDeckRepo(repo, repo), true);
  assert.equal(isDeckRepo(lookalike, repo), false);
  assert.equal(isDeckRepo(repo, ''), false);
  assert.equal(isDeckRepo('', repo), false);

  const link = path.join(tempDir(), 'link');
  fs.symlinkSync(repo, link);
  assert.equal(isDeckRepo(link, repo), true, 'symlinks to the deck repo are the deck repo');
});

test('dev task runner refuses to run outside the deck repository', () => {
  const repo = fakeRepo();
  const other = fakeRepo();
  const events = [];

  const blocked = createRunner({ getWorkspace: () => other, emit: (e) => events.push(e), repoRoot: repo });
  const refusal = blocked.start('test');
  assert.match(refusal.error, /only run in the cortex repository/);
  assert.equal(blocked.list().length, 0);
  assert.equal(events.length, 0);

  const packaged = createRunner({ getWorkspace: () => repo, emit: () => {}, repoRoot: '' });
  assert.match(packaged.start('test').error, /only run in the cortex repository/);

  const unknown = createRunner({ getWorkspace: () => repo, emit: () => {}, repoRoot: repo });
  assert.match(unknown.start('rm-rf').error, /unknown task/);
});
