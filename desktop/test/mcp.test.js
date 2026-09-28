/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { Readable, Writable } from 'node:stream';
import { createMcpClient, toolResultEnvelope, toolResultText } from '../src/main/mcp.js';

/**
 * A stub child process that speaks newline-delimited JSON-RPC: it answers
 * initialize, tools/list, and tools/call, and can emit notifications.
 */
function fakeChild() {
  const stdout = new Readable({ read() {} });
  const stderr = new Readable({ read() {} });
  const stdin = new Writable({
    write(chunk, _encoding, callback) {
      const message = JSON.parse(chunk.toString());
      received.push(message);
      setTimeout(() => respond(message), 0);
      callback();
    },
  });
  const child = Object.assign(new EventEmitter(), { stdout, stderr, stdin, pid: 4242, killed: false, kill() { this.killed = true; this.emit('close', 0, null); } });
  const received = [];

  const send = (message) => stdout.push(`${JSON.stringify(message)}\n`);

  const respond = (message) => {
    if (message.method === 'initialize') {
      send({ jsonrpc: '2.0', id: message.id, result: { protocolVersion: '2025-06-18', serverInfo: { name: 'cortex', version: 'v0.20.0' }, capabilities: { tools: {} } } });
      return;
    }
    if (message.method === 'tools/list') {
      send({
        jsonrpc: '2.0',
        id: message.id,
        result: {
          tools: [
            { name: 'cortex_status', description: 'Read task status', inputSchema: { type: 'object', properties: { taskId: { type: 'string' } }, required: ['taskId'] } },
            { name: 'cortex_sessions', description: 'List all sessions', inputSchema: { type: 'object', properties: {} } },
          ],
        },
      });
      return;
    }
    if (message.method === 'tools/call') {
      if (message.params?.name === 'cortex_fail') {
        send({ jsonrpc: '2.0', id: message.id, error: { code: -32000, message: 'kernel refused' } });
        return;
      }
      send({
        jsonrpc: '2.0',
        id: message.id,
        result: { content: [{ type: 'text', text: JSON.stringify({ ok: true, taskId: message.params?.arguments?.taskId ?? 'task_1', summary: 'called' }) }], isError: false },
      });
      return;
    }
    if (message.method === 'resources/list') {
      send({ jsonrpc: '2.0', id: message.id, error: { code: -32601, message: 'method not found' } });
    }
  };

  return { child, received, send };
}

test('start completes the MCP handshake and lists tools', async () => {
  const { child, received } = fakeChild();
  const client = createMcpClient({ resolveCommand: () => ({ binary: '/bin/true', argv: [], env: {} }), spawnFn: () => child });

  const status = await client.start({ profile: 'all', workspace: '/tmp/ws' });
  assert.equal(status.state, 'ready');
  assert.equal(status.profile, 'all');
  assert.equal(status.serverInfo.name, 'cortex');
  assert.equal(status.toolCount, 2);
  assert.deepEqual(status.tools.map((tool) => tool.name), ['cortex_status', 'cortex_sessions']);

  const initialize = received[0];
  assert.equal(initialize.method, 'initialize');
  assert.equal(initialize.params.clientInfo.name, 'cortex-deck');
  assert.ok(received.some((message) => message.method === 'notifications/initialized'), 'must notify initialized');

  const spawnArgs = client.status().workspace;
  assert.equal(spawnArgs, '/tmp/ws');
});

test('the server is started with `serve --profile <p> -C <workspace>`', async () => {
  const { child } = fakeChild();
  let captured = null;
  const client = createMcpClient({
    resolveCommand: () => ({ binary: 'cortex', argv: [], env: {} }),
    spawnFn: (binary, args) => { captured = { binary, args }; return child; },
  });
  await client.start({ profile: 'agent', workspace: '/tmp/ws' });
  assert.deepEqual(captured.args, ['serve', '--profile', 'agent', '-C', '/tmp/ws']);
});

test('tools/call returns the cortex envelope as text content', async () => {
  const { child } = fakeChild();
  const client = createMcpClient({ resolveCommand: () => ({ binary: 'cortex', argv: [], env: {} }), spawnFn: () => child });
  await client.start({});

  const result = await client.callTool('cortex_status', { taskId: 'task_7' });
  assert.equal(result.isError, false);
  assert.match(toolResultText(result), /"taskId":"task_7"/);
  const envelope = toolResultEnvelope(result);
  assert.equal(envelope.ok, true);
  assert.equal(envelope.taskId, 'task_7');
  assert.equal(envelope.summary, 'called');
});

test('a JSON-RPC error rejects the pending request', async () => {
  const { child } = fakeChild();
  const client = createMcpClient({ resolveCommand: () => ({ binary: 'cortex', argv: [], env: {} }), spawnFn: () => child });
  await client.start({});
  await assert.rejects(() => client.callTool('cortex_fail', {}), /kernel refused/);
});

test('an unsupported method surfaces the server error rather than hanging', async () => {
  const { child } = fakeChild();
  const client = createMcpClient({ resolveCommand: () => ({ binary: 'cortex', argv: [], env: {} }), spawnFn: () => child });
  await client.start({});
  await assert.rejects(() => client.request('resources/list', {}), /method not found/);
});

test('requests time out when the server never answers', async () => {
  const stdout = new Readable({ read() {} });
  const stderr = new Readable({ read() {} });
  const stdin = new Writable({ write(_c, _e, cb) { cb(); } });
  const child = Object.assign(new EventEmitter(), { stdout, stderr, stdin, pid: 1, kill() {} });

  const client = createMcpClient({ resolveCommand: () => ({ binary: 'cortex', argv: [], env: {} }), spawnFn: () => child, requestTimeoutMs: 120 });
  await assert.rejects(() => client.start({}), /timed out/);
  assert.equal(client.status().state, 'error');
});

test('stdout framing survives a message split across chunks, and non-JSON is reported', async () => {
  const { child, send } = fakeChild();
  const client = createMcpClient({ resolveCommand: () => ({ binary: 'cortex', argv: [], env: {} }), spawnFn: () => child });
  const events = [];
  client.events.on('event', (event) => events.push(event));
  await client.start({});

  const line = `${JSON.stringify({ jsonrpc: '2.0', method: 'notifications/progress', params: { progress: 1 } })}\n`;
  child.stdout.push(line.slice(0, 12));
  child.stdout.push(line.slice(12));
  child.stdout.push('not json at all\n');
  await new Promise((resolve) => setTimeout(resolve, 20));

  assert.ok(events.some((event) => event.type === 'notification' && event.method === 'notifications/progress'), 'split frames must reassemble');
  assert.ok(events.some((event) => event.type === 'protocol' && /non-JSON/.test(event.error ?? '')), 'non-JSON stdout must be reported, not parsed');
  void send;
});

test('stderr is surfaced as diagnostics, never mixed into the protocol stream', async () => {
  const { child } = fakeChild();
  const client = createMcpClient({ resolveCommand: () => ({ binary: 'cortex', argv: [], env: {} }), spawnFn: () => child });
  const stderrChunks = [];
  client.events.on('event', (event) => { if (event.type === 'stderr') stderrChunks.push(event.text); });
  await client.start({});
  child.stderr.push('cortex: listening on stdio\n');
  await new Promise((resolve) => setTimeout(resolve, 10));
  assert.deepEqual(stderrChunks, ['cortex: listening on stdio\n']);
});

test('server exit rejects pending calls and reports stopped', async () => {
  const { child } = fakeChild();
  const client = createMcpClient({ resolveCommand: () => ({ binary: 'cortex', argv: [], env: {} }), spawnFn: () => child });
  await client.start({});
  const pending = client.request('tools/list', {});
  child.emit('close', 1, null);
  await assert.rejects(() => pending, /exited/);
  assert.equal(client.status().state, 'stopped');
  assert.equal(client.status().lastError, '');
});

test('calling a tool before start fails fast', async () => {
  const client = createMcpClient({ resolveCommand: () => ({ binary: 'cortex', argv: [], env: {} }), spawnFn: () => { throw new Error('should not spawn'); } });
  await assert.rejects(() => client.callTool('cortex_status', {}), /not running/);
});

test('stop ends stdin and terminates the child', async () => {
  const { child } = fakeChild();
  const client = createMcpClient({ resolveCommand: () => ({ binary: 'cortex', argv: [], env: {} }), spawnFn: () => child });
  await client.start({});
  client.stop();
  assert.equal(child.killed, true);
  assert.equal(client.status().state, 'stopped');
});

test('toolResultText flattens text, resource, and unknown blocks', () => {
  assert.equal(toolResultText({ content: [{ type: 'text', text: 'a' }, { type: 'text', text: 'b' }] }), 'a\nb');
  assert.equal(toolResultText({ content: [{ type: 'resource', resource: { text: 'r' } }] }), 'r');
  assert.equal(toolResultText({ content: [{ type: 'image', data: 'x' }] }), '{"type":"image","data":"x"}');
  assert.equal(toolResultText(null), '');
  assert.equal(toolResultEnvelope({ content: [{ type: 'text', text: 'plain words' }] }), null);
});
