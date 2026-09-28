/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { api } from '../lib/api.js';
import { h, render } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { followAction, openCommandRunner } from '../lib/forms.js';
import { jsonView } from '../lib/jsonview.js';
import { markdown } from '../lib/markdown.js';
import { navigate } from '../lib/router.js';
import { state, selectTask } from '../lib/state.js';
import { alert, badge, button, card, copyButton, empty, kv, loading, stateBadge, table, tabs, toast } from '../lib/ui.js';
import {
  alertNote, arrayOf, coverageView, decisionCard, dossierTable, evidenceList, findingsTable, firstArray, flat,
  hypothesesList, jobsTable, leasePanel, loopDiagram, notesList, pick, receiptsList, scopePanel,
  timelineFeed, toolHealthTable, workplanView,
} from '../lib/renderers.js';
import { absoluteTime, bytes, compactHome, duration, relativeTime, shortId, sortBy, truncate } from '../lib/format.js';

const TABS = [
  { id: 'loop', label: 'Loop', icon: 'pulse' },
  { id: 'overview', label: 'Overview', icon: 'eye' },
  { id: 'timeline', label: 'Timeline', icon: 'history' },
  { id: 'evidence', label: 'Evidence', icon: 'evidence' },
  { id: 'hypotheses', label: 'Hypotheses', icon: 'target' },
  { id: 'plan', label: 'Plan', icon: 'note' },
  { id: 'verification', label: 'Verification', icon: 'shield' },
  { id: 'decisions', label: 'Decisions', icon: 'question' },
  { id: 'notes', label: 'Notes', icon: 'note' },
  { id: 'findings', label: 'Findings', icon: 'sparkles' },
  { id: 'survey', label: 'Survey & jobs', icon: 'layers' },
  { id: 'metrics', label: 'Metrics', icon: 'dashboard' },
  { id: 'resume', label: 'Resume', icon: 'bolt' },
  { id: 'handoff', label: 'Handoff', icon: 'download' },
  { id: 'files', label: 'Case files', icon: 'ledger' },
  { id: 'actions', label: 'Actions', icon: 'bolt' },
];

const PRIMARY_BY_PHASE = {
  new: { commandId: 'investigate', label: 'Investigate' },
  orienting: { commandId: 'investigate', label: 'Investigate' },
  investigating: { commandId: 'plan', label: 'Plan' },
  planned: { commandId: 'begin-change', label: 'Begin change' },
  changing: { commandId: 'verify', label: 'Verify' },
  verifying: { commandId: 'remember', label: 'Remember' },
  persisting: { commandId: 'remember', label: 'Remember' },
  blocked: { commandId: 'decision.resume', label: 'Resume decision' },
  paused: { commandId: 'decision.resume', label: 'Resume decision' },
};

export const caseRoute = {
  id: 'case',
  label: 'Case',
  icon: 'case',
  group: 'Operate',
  hint: 'One session, every projection and every write',
  async render({ params }) {
    const taskId = params.taskId ?? state.selectedTask?.taskId ?? '';
    if (!taskId) return taskPicker();

    const workspace = params.workspace || (state.selectedTask?.taskId === taskId ? state.selectedTask.workspace : '') || state.workspace;
    selectTask({ taskId, repo: params.repo, workspace, goal: params.goal });

    const root = h('div', { class: 'col', style: { gap: '14px', minWidth: '0' } });
    render(root, loading('opening the case…'));

    const runOpts = { stream: false, workspace };
    const [statusResult, showResult] = await Promise.all([
      api.run('status', { taskId, detail: 'full' }, runOpts).catch(() => null),
      api.run('show', { taskId }, runOpts).catch(() => null),
    ]);

    if (!statusResult?.ok && !showResult?.ok) {
      render(root,
        caseHeader({ taskId, workspace, phase: '', goal: '' }, null),
        alert({
          tone: 'error',
          title: 'This case could not be read',
          body: statusResult?.error ?? showResult?.error ?? 'no result',
        }),
        card({ title: 'What to try', iconName: 'wrench' },
          h('ul', { class: 'md' },
            h('li', {}, 'The session may live in a different workspace — pick it from Sessions, which locates cases across every repository.'),
            h('li', {}, 'Repo-local case stores (cases_dir) are only visible when that repository is the selected workspace.'),
            h('li', {}, 'Run doctor from the Environment view to confirm the binary and store paths.'),
          ),
        ),
      );
      return root;
    }

    const status = statusResult?.json ?? {};
    const show = showResult?.json ?? {};
    const caseJson = show.case ?? {};
    const phase = String(pick(status, 'phase', 'state') ?? pick(caseJson, 'status', 'phase') ?? '').toLowerCase();
    const goal = flat(pick(status, 'goal') ?? caseJson.goal ?? state.selectedTask?.goal ?? '');
    const assessment = pick(status, 'verificationOutcome') ?? pick(show, 'verificationAssessment')?.outcome ?? '';

    const ctx = { taskId, workspace, phase, status, show, assessment, runOpts };
    let activeTab = params.tab ?? 'loop';

    const tabHost = h('div');
    const bodyHost = h('div', { class: 'col', style: { gap: '14px', minWidth: '0' } });

    const paintTabs = () => {
      render(tabHost, tabs(TABS, activeTab, async (id) => {
        activeTab = id;
        paintTabs();
        await paintBody();
      }));
    };

    const paintBody = async () => {
      render(bodyHost, loading(`loading ${activeTab}…`));
      try {
        const node = await TAB_RENDERERS[activeTab](ctx);
        render(bodyHost, node ?? empty({ title: 'Nothing to show' }));
      } catch (err) {
        render(bodyHost, alert({ tone: 'error', title: `Failed to render ${activeTab}`, body: String(err?.message ?? err) }));
      }
    };

    render(root,
      caseHeader({ taskId, workspace, phase, goal, assessment }, ctx, () => paintBody()),
      h('div', { class: 'case-tabs' }, tabHost),
      bodyHost,
    );
    paintTabs();
    await paintBody();
    return root;
  },
};

/* ── Header ────────────────────────────────────────────────────────────── */

function caseHeader(info, ctx, refresh) {
  const primary = PRIMARY_BY_PHASE[info.phase] ?? null;
  const lease = pick(ctx?.status, 'lease', 'changeLease') ?? pick(ctx?.show?.case, 'changeLease') ?? null;
  return h('div', { class: 'case-head' },
    h('div', { class: 'row-wrap', style: { gap: '8px' } },
      stateBadge(info.phase || 'unknown'),
      info.assessment ? stateBadge(info.assessment) : null,
      lease?.actor ? badge(`lease: ${lease.actor}`, 'amber') : null,
      h('button', { class: 'case-id', title: 'Copy task id', onclick: () => copyTaskId(info.taskId) }, info.taskId),
      h('span', { class: 'grow' }),
      primary ? button(primary.label, { kind: 'primary', iconName: 'bolt', onClick: () => openCommandRunner(primary.commandId, { values: { taskId: info.taskId }, workspace: info.workspace, onDone: refresh }) }) : null,
      button('Investigate', { iconName: 'search', onClick: () => openCommandRunner('investigate', { values: { taskId: info.taskId }, workspace: info.workspace, onDone: refresh }) }),
      button('Note', { iconName: 'note', onClick: () => openCommandRunner('note', { values: { taskId: info.taskId }, workspace: info.workspace, onDone: refresh }) }),
      button('Refresh', { iconName: 'refresh', onClick: () => refresh?.() }),
    ),
    h('div', { class: 'case-goal', text: truncate(info.goal, 260) || '(no goal recorded)' }),
    h('div', { class: 'case-meta' },
      h('span', { class: 'mono-sm faint', text: compactHome(info.workspace) }),
      pick(ctx?.status, 'mode') ? badge(String(pick(ctx.status, 'mode')), 'violet') : null,
      pick(ctx?.status, 'risk') ? badge(`risk: ${pick(ctx.status, 'risk')}`, 'slate') : null,
      pick(ctx?.status, 'revision') ? badge(`rev ${pick(ctx.status, 'revision')}`, 'slate') : null,
      pick(ctx?.show, 'elapsedMs') !== undefined ? badge(`elapsed ${duration(Number(pick(ctx.show, 'elapsedMs')))}`, 'slate') : null,
    ),
    loopDiagram(info.phase),
  );
}

async function copyTaskId(taskId) {
  await navigator.clipboard?.writeText(taskId);
  toast('ok', 'Task id copied', taskId, 2200);
}

/* ── Tab renderers ─────────────────────────────────────────────────────── */

const TAB_RENDERERS = {
  async loop(ctx) {
    const status = ctx.status ?? {};
    const warnings = firstArray(status, 'warnings');
    const missing = firstArray(status, 'missingVerification');
    const hypotheses = firstArray(status, 'unresolvedHypotheses');
    const tools = firstArray(status, 'toolHealth');
    const surfaces = firstArray(status, 'surfaces');
    const required = firstArray(status, 'verificationRequired');
    const caseFile = await api.store.file(ctx.taskId, 'case.json').catch(() => null);
    const caseJson = caseFile?.json ?? {};
    const lease = caseJson.changeLease ?? pick(status, 'lease', 'changeLease') ?? null;
    const criteria = firstArray(caseJson, 'acceptance', 'acceptanceCriteria', 'criteria');

    return h('div', { class: 'col', style: { gap: '14px' } },
      warnings.length
        ? alert({ tone: 'warn', title: `${warnings.length} kernel warning${warnings.length === 1 ? '' : 's'}`, body: warnings.map(flat).join(' · ') })
        : null,
      h('div', { class: 'grid grid-2' },
        card({ title: 'Canonical assessment', sub: 'what remember is allowed to claim', iconName: 'shield' },
          kv([
            ['Assessment', stateBadge(pick(status, 'verificationOutcome') ?? 'unverified')],
            ['Phase', stateBadge(pick(status, 'phase', 'state') ?? '')],
            ['Mode', pick(status, 'mode') ?? '—'],
            ['Risk', pick(status, 'risk') ?? '—'],
            ['Revision', pick(status, 'revision') ?? '—'],
            ['Surfaces', surfaces.length ? h('div', { class: 'pill-row' }, surfaces.map((s) => badge(flat(s), 'info'))) : '—'],
            ['Evidence', `${pick(status, 'evidenceCount') ?? 0} records · ${pick(status, 'investigationRounds') ?? 0} round(s) of ${pick(status, 'investigationBudget') ?? '?'}`],
            ['Proof', `${pick(status, 'claimProofTotal') ?? 0} bound receipts · ${required.length} required verifier(s)`],
          ]),
          required.length
            ? h('div', { style: { marginTop: '10px' } },
                h('div', { class: 'micro', text: 'verification required by the plan' }),
                h('div', { class: 'pill-row', style: { marginTop: '5px' } }, required.map((r) => badge(flat(r), 'violet'))))
            : null,
          criteria.length
            ? h('div', { style: { marginTop: '10px' } },
                h('div', { class: 'micro', text: 'immutable acceptance criteria' }),
                h('div', { class: 'col', style: { gap: '4px', marginTop: '5px' } }, criteria.map((c) =>
                  h('div', { class: 'list-item' },
                    h('span', { class: 'mono-sm accent-text', text: flat(pick(c, 'id', 'criterionId')) }),
                    h('span', { class: 'grow tiny', text: truncate(flat(pick(c, 'statement', 'text')), 160) }),
                  ))))
            : null,
        ),
        card({
          title: 'Change lease',
          sub: 'bounded, expiring ownership — competing actors lose the CAS race',
          iconName: 'clock',
          actions: [
            button('Begin…', { size: 'sm', iconName: 'bolt', onClick: () => openCommandRunner('begin-change', { values: { taskId: ctx.taskId }, workspace: ctx.workspace }) }),
            lease ? button('Renew', { size: 'sm', iconName: 'refresh', onClick: () => openCommandRunner('lease.renew', { values: { taskId: ctx.taskId }, workspace: ctx.workspace }) }) : null,
            lease ? button('Release', { size: 'sm', iconName: 'stop', onClick: () => openCommandRunner('lease.release', { values: { taskId: ctx.taskId }, workspace: ctx.workspace }) }) : null,
          ],
        }, leasePanel(lease)),
      ),
      card({ title: 'Structured next actions', sub: 'the kernel’s machine-readable continuation — click to run it prefilled', iconName: 'arrowRight' }, actionsBoard(ctx)),
      h('div', { class: 'grid grid-2' },
        card({ title: 'Missing verification', sub: `${missing.length} requirement(s) without a receipt`, iconName: 'alert' },
          missing.length
            ? h('div', { class: 'col', style: { gap: '6px' } }, missing.map((item) => h('div', { class: 'list-item' }, icon('alert', 13), h('span', { class: 'grow', text: flat(item) }))))
            : alert({ tone: 'ok', title: 'Nothing missing', body: 'Every declared requirement has a receipt, or none was declared.' })),
        card({ title: 'Scope', sub: 'declared boundary vs. what actually changed', iconName: 'target' },
          scopePanel(pick(status, 'scope', 'scopeDrift') ?? ctx.scope ?? null)
            ?? h('div', { class: 'col', style: { gap: '10px' } },
              boundaryPanel(caseJson.changeBoundary ?? pick(status, 'changeBoundary')),
              alertNote('No drift computed yet', 'Scope drift is derived from git once a change exists; verify reports it in its receipt.'))),
      ),
      hypotheses.length
        ? card({ title: 'Open hypotheses', sub: `${hypotheses.length} unresolved — each carries a disproof path`, iconName: 'target', flush: true },
            h('div', { class: 'card-body' }, hypothesesList(hypotheses, { onResolve: (hyp) => openResolve(ctx, hyp) })))
        : null,
      tools.length
        ? card({ title: 'Discovery readiness', sub: 'status --detail full probes every specialist concurrently', iconName: 'cpu', flush: true },
            h('div', { class: 'card-body' }, toolHealthTable(tools)))
        : null,
      card({ title: 'Raw status payload', sub: 'exactly what cortex status --detail full --json returned', iconName: 'ledger', flush: true },
        h('div', { class: 'card-body' }, jsonView(status, { label: 'status', depth: 1, collapsed: true }))),
    );
  },

  async overview(ctx) {
    const show = ctx.show ?? {};
    const caseJson = show.case ?? {};
    const phases = firstArray(show, 'phaseDurations', 'timeInPhase', 'phases');
    const activity = firstArray(show, 'timeline', 'recent', 'recentActivity');
    const evidence = firstArray(show, 'evidence');
    const hypotheses = firstArray(show, 'hypotheses');
    const assessment = show.verificationAssessment ?? {};
    const plan = show.plan ?? {};
    const boundary = caseJson.changeBoundary ?? plan.changeBoundary ?? {};
    const elapsed = pick(show, 'elapsedMs');
    const receiptsNode = await receiptsHost(ctx);

    return h('div', { class: 'col', style: { gap: '14px' } },
      card({ title: 'Case identity', sub: compactHome(flat(pick(caseJson, 'workspace')?.root ?? caseJson.workspace ?? '')), iconName: 'case' },
        kv([
          ['Goal', truncate(flat(caseJson.goal), 220)],
          ['Status', stateBadge(pick(caseJson, 'status', 'phase') ?? '')],
          ['Mode / risk', `${caseJson.mode ?? '—'} / ${caseJson.risk ?? '—'}`],
          ['Repository', `${flat(pick(caseJson, 'workspace')?.repository ?? '')} @ ${flat(pick(caseJson, 'workspace')?.branch ?? '')}`],
          ['Opened', `${absoluteTime(caseJson.createdAt)} (${relativeTime(caseJson.createdAt)})`],
          ['Updated', `${absoluteTime(caseJson.updatedAt)} (${relativeTime(caseJson.updatedAt)})`],
          ['Elapsed', elapsed !== undefined ? duration(Number(elapsed)) : '—'],
          ['Boundary', `${firstArray(boundary, 'files').length} files · ${firstArray(boundary, 'symbols').length} symbols`],
        ])),
      h('div', { class: 'grid grid-2' },
        card({ title: 'Time in phase', sub: 'where the case actually spent its time', iconName: 'clock' },
          phases.length
            ? h('div', { class: 'col', style: { gap: '8px' } }, phases.map((entry) => {
                const name = flat(pick(entry, 'phase', 'name', 'from')) || '';
                const ms = Number(pick(entry, 'ms', 'durationMs', 'elapsedMs') ?? 0);
                const share = elapsed ? Math.min(100, (ms / Number(elapsed)) * 100) : 0;
                return h('div', { class: 'col', style: { gap: '3px' } },
                  h('div', { class: 'spread' }, h('span', { class: 'row', style: { gap: '7px' } }, stateBadge(name)), h('span', { class: 'mono-sm', text: duration(ms) || '—' })),
                  h('div', { class: 'bar', dataset: { tone: 'violet' } }, h('span', { style: { width: `${share}%` } })),
                );
              }))
            : empty({ iconName: 'clock', title: 'No phase timings', note: 'A case that never transitioned has nothing to report.' })),
        card({ title: 'Ledger totals', sub: 'exact counts from one task-locked snapshot', iconName: 'ledger' },
          kv([
            ['Evidence', `${firstArray(show, 'evidence').length} shown of ${show.evidenceTotal ?? '?'} total`],
            ['Receipts', String(show.receiptTotal ?? 0)],
            ['Decisions', String(show.decisionTotal ?? 0)],
            ['Timeline', `${firstArray(show, 'timeline').length} shown of ${show.timelineTotal ?? '?'} events`],
            ['Assessment', stateBadge(pick(assessment, 'outcome') ?? 'unverified')],
            ['Missing proof', firstArray(assessment, 'missingRequired').length ? badge(`${firstArray(assessment, 'missingRequired').length} required`, 'amber') : badge('none', 'green')],
          ])),
      ),
      plan.uncertainty
        ? card({ title: 'Stated uncertainty', sub: 'required by the planning gate', iconName: 'question' }, h('div', { class: 'md', style: { fontSize: 'var(--fs-12)' }, text: flat(plan.uncertainty) }))
        : null,
      card({ title: 'Verification receipts', sub: 'from verification.json', iconName: 'shield', flush: true }, receiptsNode),
      card({ title: 'Hypotheses', sub: `${hypotheses.length} recorded`, iconName: 'target', flush: true },
        h('div', { class: 'card-body' }, hypothesesList(hypotheses, { onResolve: (hyp) => openResolve(ctx, hyp) }))),
      card({ title: 'Recent evidence', sub: `bounded projection of ${show.evidenceTotal ?? 0} records`, iconName: 'evidence', flush: true },
        h('div', { class: 'card-body' }, evidenceList(evidence))),
      card({ title: 'Recent activity', sub: `bounded projection of ${show.timelineTotal ?? 0} events`, iconName: 'history', flush: true },
        h('div', { class: 'card-body' }, timelineFeed(activity, { limit: 40 }))),
      card({ title: 'Raw show payload', iconName: 'ledger', flush: true }, h('div', { class: 'card-body' }, jsonView(show, { label: 'show', depth: 1, collapsed: true }))),
    );
  },

  async timeline(ctx) {
    const result = await api.run('timeline', { taskId: ctx.taskId }, ctx.runOpts);
    if (!result.ok && !result.json) return alert({ tone: 'error', title: 'timeline failed', body: result.error ?? '' });
    const json = result.json ?? {};
    const entries = firstArray(json, 'events', 'entries', 'timeline', 'items');
    const kinds = [...new Set(entries.map((e) => String(pick(e, 'kind', 'type', 'category') ?? 'event').toLowerCase()))];
    let filter = '';
    const host = h('div');
    const paint = () => render(host, timelineFeed(entries, { filter, limit: 600 }));
    paint();
    return h('div', { class: 'col', style: { gap: '12px' } },
      card({ title: 'Chronological audit trail', sub: `${entries.length} events · phases, evidence, tool calls, verification`, iconName: 'history' },
        h('div', { class: 'filter-bar' },
          h('span', { class: 'micro', text: 'filter' }),
          ...kinds.map((kind) => h('button', {
            class: 'btn btn-sm',
            dataset: { active: String(filter === kind) },
            onclick: (event) => { filter = filter === kind ? '' : kind; for (const node of event.currentTarget.parentElement.querySelectorAll('.btn')) node.dataset.active = 'false'; event.currentTarget.dataset.active = String(Boolean(filter)); paint(); },
          }, kind)),
          h('span', { class: 'grow' }),
          copyButton(() => JSON.stringify(json, null, 2), 'Copy timeline JSON'),
        ),
      ),
      host,
      card({ title: 'Raw timeline payload', iconName: 'ledger', flush: true }, h('div', { class: 'card-body' }, jsonView(json, { label: 'timeline', depth: 1, collapsed: true }))),
    );
  },

  async evidence(ctx) {
    const file = await api.store.file(ctx.taskId, 'evidence.jsonl').catch(() => null);
    const records = (file?.records?.records ?? []).map((r) => r.value);
    let query = '';
    const host = h('div');
    const counts = countBy(records, (e) => String(pick(e, 'kind', 'type') ?? 'unknown'));
    const confidences = countBy(records, (e) => String(pick(e, 'confidence') ?? 'unknown'));

    const paint = () => {
      const filtered = query
        ? records.filter((e) => JSON.stringify(e).toLowerCase().includes(query.toLowerCase()))
        : records;
      render(host, h('div', { class: 'col', style: { gap: '12px' } },
        h('div', { class: 'row-wrap' },
          badge(`${filtered.length} records`, 'info'),
          ...Object.entries(confidences).map(([key, value]) => badge(`${key}: ${value}`, key === 'high' ? 'green' : key === 'medium' ? 'info' : 'slate')),
        ),
        evidenceList(filtered, { onOpen: (e) => openEvidence(ctx, e), onPreview: (e) => openArtifact(ctx, e) }),
        file?.corrupt?.length ? alert({ tone: 'warn', title: `${file.corrupt.length} corrupt ledger lines`, body: 'Cross-workspace listings surface unreadable records as errors instead of hiding them.' }) : null,
      ));
    };
    paint();

    return h('div', { class: 'col', style: { gap: '14px' } },
      card({ title: 'Evidence ledger', sub: `${records.length} records in evidence.jsonl${file?.truncated ? ' (truncated read)' : ''}`, iconName: 'evidence', flush: true },
        h('div', { class: 'card-body col', style: { gap: '10px' } },
          h('div', { class: 'filter-bar' },
            h('input', { class: 'input', style: { minWidth: '260px' }, placeholder: 'filter by claim, source, id, uri…', oninput: (event) => { query = event.target.value; paint(); } }),
            h('span', { class: 'grow' }),
            h('div', { class: 'pill-row' }, Object.entries(counts).slice(0, 8).map(([key, value]) => badge(`${key} ${value}`, 'slate'))),
            button('Read evidence…', { size: 'sm', iconName: 'eye', onClick: () => openCommandRunner('read-evidence', { values: { taskId: ctx.taskId }, workspace: ctx.workspace }) }),
            button('Read artifact…', { size: 'sm', iconName: 'file', onClick: () => openCommandRunner('read-artifact', { values: { taskId: ctx.taskId }, workspace: ctx.workspace }) }),
          ),
        ),
      ),
      host,
    );
  },

  async hypotheses(ctx) {
    const file = await api.store.file(ctx.taskId, 'hypotheses.json').catch(() => null);
    const stored = file?.json;
    const list = firstArray(stored, 'hypotheses', 'items') || arrayOf(stored);
    const fromStatus = firstArray(ctx.status, 'hypotheses', 'unresolvedHypotheses');
    const items = list.length ? list : fromStatus;
    return h('div', { class: 'col', style: { gap: '14px' } },
      card({
        title: 'Hypotheses',
        sub: 'the disproof-path gate — a plan without one is rejected',
        iconName: 'target',
        actions: [
          button('Resolve…', { size: 'sm', iconName: 'check', onClick: () => openCommandRunner('resolve', { values: { taskId: ctx.taskId }, workspace: ctx.workspace }) }),
          button('Plan…', { size: 'sm', iconName: 'note', onClick: () => openCommandRunner('plan', { values: { taskId: ctx.taskId }, workspace: ctx.workspace }) }),
        ],
      }, hypothesesList(items, { onResolve: (hyp) => openResolve(ctx, hyp) })),
      stored ? card({ title: 'hypotheses.json', iconName: 'ledger', flush: true }, h('div', { class: 'card-body' }, jsonView(stored, { label: 'hypotheses.json', depth: 2 }))) : null,
    );
  },

  async plan(ctx) {
    const file = await api.store.file(ctx.taskId, 'plan.json').catch(() => null);
    const plan = file?.json ?? null;
    if (!plan) return empty({ iconName: 'note', title: 'No plan recorded', note: 'cortex plan stores the planning gate: hypotheses with disproof paths, a change boundary, uncertainty, and required verifiers.' });
    const boundary = pick(plan, 'boundary') ?? {};
    const files = firstArray(boundary, 'files', 'file');
    const symbols = firstArray(boundary, 'symbols', 'symbol');
    const verifiers = firstArray(plan, 'requiredVerification', 'verify', 'verifiers', 'verification');
    return h('div', { class: 'col', style: { gap: '14px' } },
      h('div', { class: 'grid grid-2' },
        card({ title: 'Change boundary', sub: pick(boundary, 'reason') ?? 'why these files/symbols are the expected change set', iconName: 'target' },
          files.length || symbols.length
            ? h('div', { class: 'col', style: { gap: '10px' } },
                files.length ? h('div', {}, h('div', { class: 'micro', text: 'files' }), h('div', { class: 'pill-row', style: { marginTop: '5px' } }, files.map((f) => h('span', { class: 'tag', text: String(typeof f === 'string' ? f : pick(f, 'path') ?? JSON.stringify(f)) })))) : null,
                symbols.length ? h('div', {}, h('div', { class: 'micro', text: 'symbols' }), h('div', { class: 'pill-row', style: { marginTop: '5px' } }, symbols.map((s) => h('span', { class: 'tag', text: String(typeof s === 'string' ? s : pick(s, 'name') ?? JSON.stringify(s)) })))) : null,
              )
            : alert({ tone: 'warn', title: 'No boundary declared', body: 'Change tasks must declare one; the planning gate rejects plans without it.' })),
        card({ title: 'Required verification', sub: 'surfaces and verifiers this plan promises', iconName: 'shield' },
          verifiers.length
            ? h('div', { class: 'pill-row' }, verifiers.map((v) => badge(typeof v === 'string' ? v : pick(v, 'verifier', 'name', 'kind') ?? JSON.stringify(v), 'violet')))
            : alert({ title: 'Nothing required', body: 'This plan declared no verifier requirements.' })),
      ),
      card({ title: 'Stated uncertainty', sub: 'required by the planning gate', iconName: 'question' },
        h('div', { class: 'md', style: { fontSize: 'var(--fs-12)' }, text: String(pick(plan, 'uncertainty') ?? '—') })),
      card({
        title: 'Hypotheses in this plan',
        iconName: 'target',
        actions: [button('Edit plan…', { size: 'sm', onClick: () => openCommandRunner('plan', { values: { taskId: ctx.taskId }, workspace: ctx.workspace }) })],
      }, hypothesesList(firstArray(plan, 'hypotheses'), { onResolve: (hyp) => openResolve(ctx, hyp) })),
      card({ title: 'plan.json', iconName: 'ledger', flush: true }, h('div', { class: 'card-body' }, jsonView(plan, { label: 'plan.json', depth: 2 }))),
    );
  },

  async verification(ctx) {
    const file = await api.store.file(ctx.taskId, 'verification.json').catch(() => null);
    const stored = file?.json ?? null;
    const receipts = firstArray(stored, 'receipts', 'batches', 'records', 'items');
    const criteria = firstArray(ctx.status, 'acceptanceCriteria', 'criteria') || firstArray(ctx.show, 'acceptanceCriteria', 'criteria');
    return h('div', { class: 'col', style: { gap: '14px' } },
      card({
        title: 'Verification',
        sub: 'typed claim → surface → verifier → receipt',
        iconName: 'shield',
        actions: [
          button('Verify…', { size: 'sm', kind: 'primary', iconName: 'shield', onClick: () => openCommandRunner('verify', { values: { taskId: ctx.taskId }, workspace: ctx.workspace }) }),
          button('From plan', { size: 'sm', onClick: () => openCommandRunner('verify', { values: { taskId: ctx.taskId, 'from-plan': true }, workspace: ctx.workspace }) }),
        ],
      }, receiptsList(receipts)),
      criteria.length
        ? card({ title: 'Acceptance criteria', sub: 'immutable case identity — verification must reuse the exact id and statement', iconName: 'check' },
            h('div', { class: 'col', style: { gap: '6px' } }, criteria.map((criterion) => h('div', { class: 'list-item' },
              h('span', { class: 'mono-sm accent-text', text: String(pick(criterion, 'id', 'criterionId') ?? '') }),
              h('span', { class: 'grow', text: truncate(String(pick(criterion, 'statement', 'text') ?? JSON.stringify(criterion)), 220) }),
              pick(criterion, 'proven') ? stateBadge('proven') : null,
            ))))
        : null,
      stored ? card({ title: 'verification.json', iconName: 'ledger', flush: true }, h('div', { class: 'card-body' }, jsonView(stored, { label: 'verification.json', depth: 2 }))) : null,
    );
  },

  async decisions(ctx) {
    const file = await api.store.file(ctx.taskId, 'decisions.json').catch(() => null);
    const stored = file?.json;
    const items = firstArray(stored, 'decisions', 'items') || arrayOf(stored).filter((x) => typeof x === 'object');
    const pending = items.filter((d) => ['pending', 'open', ''].includes(String(pick(d, 'status', 'state') ?? '').toLowerCase()));
    return h('div', { class: 'col', style: { gap: '14px' } },
      card({
        title: 'Bounded human decisions',
        sub: `${items.length} recorded · ${pending.length} awaiting an answer`,
        iconName: 'question',
        actions: [
          button('Request…', { size: 'sm', iconName: 'plus', onClick: () => openCommandRunner('decision.request', { values: { taskId: ctx.taskId }, workspace: ctx.workspace }) }),
          button('Recover', { size: 'sm', iconName: 'refresh', onClick: () => openCommandRunner('decision.resume', { values: { taskId: ctx.taskId }, workspace: ctx.workspace }) }),
        ],
      },
        items.length
          ? h('div', { class: 'col', style: { gap: '10px' } }, items.map((decision) => decisionCard(decision, { onAnswer: (d, optionId) => answerDecision(ctx, d, optionId) })))
          : empty({ iconName: 'question', title: 'No decisions', note: 'A decision pauses the task on one bounded question with explicit consequences for each option.' })),
    );
  },

  async notes(ctx) {
    const [notesFile, caseFile] = await Promise.all([
      api.store.file(ctx.taskId, 'notes.jsonl').catch(() => null),
      api.store.file(ctx.taskId, 'case.json').catch(() => null),
    ]);
    let items = (notesFile?.records?.records ?? []).map((r) => r.value);
    if (!items.length) {
      items = firstArray(caseFile?.json, 'notes', 'observations');
    }
    return card({
      title: 'Notes',
      sub: 'provenance-bearing observations — never treated as verification',
      iconName: 'note',
      actions: [button('Add note…', { size: 'sm', kind: 'primary', iconName: 'plus', onClick: () => openCommandRunner('note', { values: { taskId: ctx.taskId }, workspace: ctx.workspace }) })],
    }, notesList(sortBy(items, (n) => pick(n, 'at', 'createdAt') ?? ''), { onAddSimilar: () => openCommandRunner('note', { values: { taskId: ctx.taskId }, workspace: ctx.workspace }) }));
  },

  async findings(ctx) {
    const result = await api.run('finding.list', { taskId: ctx.taskId }, ctx.runOpts);
    const rows = firstArray(result?.json, 'findings', 'items') || arrayOf(result?.json).filter((x) => typeof x === 'object');
    const stored = await api.store.file(ctx.taskId, 'findings.json').catch(() => null);
    const fallback = firstArray(stored?.json, 'findings', 'items');
    const items = rows.length ? rows : fallback;
    return h('div', { class: 'col', style: { gap: '14px' } },
      card({
        title: 'Findings',
        sub: 'durable backlog backed by case evidence — dismissals keep their reason for recall',
        iconName: 'sparkles',
        actions: [button('Record finding…', { size: 'sm', kind: 'primary', iconName: 'plus', onClick: () => openCommandRunner('finding.add', { values: { taskId: ctx.taskId }, workspace: ctx.workspace }) })],
        flush: true,
      }, findingsTable(items, { onAction: (action, finding) => findingAction(ctx, action, finding) })),
      !result?.ok && result?.error ? alert({ tone: 'warn', title: 'finding list refused', body: result.error }) : null,
    );
  },

  async survey(ctx) {
    const [coverage, workplan, jobs, dossier] = await Promise.all([
      api.run('coverage', { taskId: ctx.taskId, all: true }, ctx.runOpts).catch(() => null),
      api.run('workplan.list', { taskId: ctx.taskId }, ctx.runOpts).catch(() => null),
      api.run('job.list', { taskId: ctx.taskId }, ctx.runOpts).catch(() => null),
      api.run('dossier.list', {}, ctx.runOpts).catch(() => null),
    ]);
    const dossierRows = firstArray(dossier?.json, 'entries', 'items') || arrayOf(dossier?.json).filter((x) => typeof x === 'object');
    return h('div', { class: 'col', style: { gap: '14px' } },
      card({
        title: 'Survey coverage',
        sub: 'which modules were explored, summarized, or never seen — budgeted per module',
        iconName: 'layers',
        actions: [
          button('Investigate module…', { size: 'sm', iconName: 'search', onClick: () => openCommandRunner('investigate', { values: { taskId: ctx.taskId, module: '' }, workspace: ctx.workspace }) }),
          button('Fan out…', { size: 'sm', iconName: 'bolt', onClick: () => openCommandRunner('investigate', { values: { taskId: ctx.taskId, fanout: true }, workspace: ctx.workspace }) }),
        ],
        flush: true,
      }, h('div', { class: 'card-body' }, coverage?.json ? coverageView(coverage.json) : empty({ title: 'No coverage ledger', note: coverage?.error ?? 'Only survey-mode cases build one.' }))),
      card({
        title: 'Campaign workplan',
        sub: 'dependency-aware child work handed out as linked, retry-keyed cases',
        iconName: 'jobs',
        actions: [
          button('Add item…', { size: 'sm', iconName: 'plus', onClick: () => openCommandRunner('workplan.add', { values: { taskId: ctx.taskId }, workspace: ctx.workspace }) }),
          button('Claim next…', { size: 'sm', iconName: 'arrowRight', onClick: () => openCommandRunner('workplan.next', { values: { taskId: ctx.taskId }, workspace: ctx.workspace }) }),
        ],
        flush: true,
      }, h('div', { class: 'card-body' }, workplan?.json ? workplanView(workplan.json, { onOpen: (id) => navigate('case', { taskId: id, workspace: ctx.workspace }) }) : empty({ title: 'No workplan', note: workplan?.error ?? '' }))),
      card({
        title: 'Background jobs',
        sub: 'detached investigations that outlive a single call',
        iconName: 'bolt',
        actions: [button('Async investigate…', { size: 'sm', iconName: 'bolt', onClick: () => openCommandRunner('investigate', { values: { taskId: ctx.taskId, async: true }, workspace: ctx.workspace }) })],
        flush: true,
      }, h('div', { class: 'card-body' }, jobsTable(firstArray(jobs?.json, 'jobs', 'items'), { onCancel: (job) => openCommandRunner('job.cancel', { values: { taskId: ctx.taskId, jobId: String(pick(job, 'id', 'jobId') ?? '') }, workspace: ctx.workspace }) }))),
      card({
        title: 'Repository dossier',
        sub: 'memory that outlives cases and goes stale when its files change',
        iconName: 'database',
        actions: [
          button('Add entry…', { size: 'sm', iconName: 'plus', onClick: () => openCommandRunner('dossier.add', { values: { taskId: ctx.taskId }, workspace: ctx.workspace }) }),
          button('Refresh', { size: 'sm', iconName: 'refresh', onClick: () => openCommandRunner('dossier.refresh', {}, { workspace: ctx.workspace }) }),
        ],
        flush: true,
      }, h('div', { class: 'card-body' }, dossierTable(dossierRows))),
    );
  },

  async metrics(ctx) {
    const [task, workspaceMetrics] = await Promise.all([
      api.run('metrics', { taskId: ctx.taskId }, ctx.runOpts).catch(() => null),
      api.run('metrics', {}, ctx.runOpts).catch(() => null),
    ]);
    const json = task?.json ?? {};
    const tiles = [
      ['Tool calls', pick(json, 'toolCalls')],
      ['Tool errors', pick(json, 'toolErrors')],
      ['Calls before first evidence', pick(json, 'callsBeforeFirstEvidence')],
      ['Evidence items', pick(json, 'evidenceItems')],
      ['Investigation rounds', pick(json, 'investigationRounds')],
      ['Hypotheses', pick(json, 'hypotheses')],
      ['Unresolved', pick(json, 'unresolvedHypotheses')],
      ['Receipts', pick(json, 'receipts', 'receiptTotal')],
    ].filter(([, value]) => value !== undefined);
    const contributions = firstArray(json, 'toolContribution', 'toolContributions', 'contributions');
    const phases = firstArray(json, 'phaseDurations');
    const missing = firstArray(json, 'missingVerification');
    const surfaces = firstArray(json, 'surfaces');
    const aggregate = workspaceMetrics?.json ?? {};

    return h('div', { class: 'col', style: { gap: '14px' } },
      tiles.length
        ? h('div', { class: 'grid grid-4' }, tiles.map(([label, value]) => h('div', {
            class: 'card stat',
            dataset: { tone: label === 'Tool errors' && Number(value) > 0 ? 'red' : label === 'Unresolved' && Number(value) > 0 ? 'amber' : '' },
          },
            h('div', { class: 'stat-label', text: label }),
            h('div', { class: 'stat-value', style: { fontSize: 'var(--fs-22)' }, text: String(value) }),
          )))
        : null,
      h('div', { class: 'grid grid-2' },
        card({ title: 'Outcome', sub: 'the canonical assessment behind remember’s gate', iconName: 'shield' },
          kv([
            ['Status', stateBadge(pick(json, 'status', 'phase') ?? '')],
            ['Complete', pick(json, 'complete') === true ? badge('yes', 'green') : badge('no', 'slate')],
            ['Verified', pick(json, 'verified') === true ? badge('yes', 'green') : badge('no', 'slate')],
            ['Verification outcome', stateBadge(pick(json, 'verificationOutcome') ?? 'unverified')],
            ['Scope drifted', pick(json, 'scopeDrifted') === true ? badge('drift', 'red') : badge('none', 'green')],
            ['Memory reused', pick(json, 'memoryReused') === true ? badge('yes', 'accent') : badge('no', 'slate')],
            ['Surfaces', surfaces.length ? h('div', { class: 'pill-row' }, surfaces.map((s) => badge(flat(s), 'info'))) : '—'],
          ]),
          missing.length
            ? h('div', { style: { marginTop: '10px' } },
                h('div', { class: 'micro danger-text', text: `${missing.length} missing verification(s)` }),
                h('div', { class: 'pill-row', style: { marginTop: '5px' } }, missing.slice(0, 24).map((m) => h('span', { class: 'tag', text: truncate(flat(m), 44) }))))
            : null),
        card({ title: 'Time in phase', sub: 'from metrics --json', iconName: 'clock' },
          phases.length
            ? h('div', { class: 'col', style: { gap: '7px' } }, phases.map((entry) => h('div', { class: 'spread' },
                h('span', { class: 'row', style: { gap: '7px' } }, stateBadge(flat(pick(entry, 'phase', 'name')))),
                h('span', { class: 'mono-sm', text: duration(Number(pick(entry, 'ms', 'durationMs') ?? 0)) || '—' },
                ))))
            : empty({ title: 'No phase timings' })),
      ),
      card({ title: 'Tool contribution', sub: 'how many hypotheses each tool’s evidence supported', iconName: 'cpu', flush: true },
        contributions.length
          ? h('div', { class: 'table-wrap' }, table({
              rows: contributions,
              columns: [
                { key: 'tool', label: 'Tool', strong: true, mono: true, width: '140px', render: (c) => flat(pick(c, 'tool', 'name')) },
                { key: 'calls', label: 'Calls', width: '80px', align: 'right', render: (c) => String(pick(c, 'calls') ?? 0) },
                { key: 'errors', label: 'Errors', width: '80px', align: 'right', render: (c) => String(pick(c, 'errors') ?? 0) },
                { key: 'evidenceItems', label: 'Evidence', width: '96px', align: 'right', render: (c) => String(pick(c, 'evidenceItems') ?? 0) },
                { key: 'hypothesesSupported', label: 'Hypotheses supported', width: '170px', align: 'right', render: (c) => String(pick(c, 'hypothesesSupported') ?? 0) },
              ],
            }))
          : empty({ title: 'No tool contributions recorded' })),
      card({ title: 'This task', sub: 'cortex metrics <taskId> --json', iconName: 'dashboard', flush: true }, h('div', { class: 'card-body' }, jsonView(json, { label: 'metrics', depth: 2 }))),
      card({ title: 'Workspace aggregate', sub: 'cortex metrics --json (no task id)', iconName: 'workspace', flush: true }, h('div', { class: 'card-body' }, jsonView(aggregate, { label: 'metrics aggregate', depth: 2 }))),
    );
  },

  async resume(ctx) {
    const result = await api.run('resume', { taskId: ctx.taskId, limit: 50 }, ctx.runOpts);
    const json = result?.json ?? {};
    const deltas = firstArray(json, 'deltas', 'changes', 'since', 'records');
    const checkpoint = pick(json, 'checkpoint', 'packet') ?? json;
    return h('div', { class: 'col', style: { gap: '14px' } },
      card({
        title: 'Recovery checkpoint',
        sub: 'what an agent reads after context loss — packet plus deltas since a cursor',
        iconName: 'bolt',
        actions: [
          copyButton(() => JSON.stringify(json, null, 2), 'Copy packet'),
          button('Since cursor…', { size: 'sm', iconName: 'history', onClick: () => openCommandRunner('resume', { values: { taskId: ctx.taskId }, workspace: ctx.workspace }) }),
        ],
        flush: true,
      }, h('div', { class: 'card-body' }, jsonView(checkpoint, { label: 'checkpoint', depth: 2 }))),
      deltas.length
        ? card({ title: 'Deltas', sub: `${deltas.length} records after the cursor`, iconName: 'history', flush: true }, h('div', { class: 'card-body' }, timelineFeed(deltas, { limit: 200 })))
        : null,
    );
  },

  async handoff(ctx) {
    const [full, compact] = await Promise.all([
      api.run('handoff', { taskId: ctx.taskId }, ctx.runOpts).catch(() => null),
      api.run('handoff', { taskId: ctx.taskId, compact: true }, ctx.runOpts).catch(() => null),
    ]);
    const markdownText = pick(full?.json, 'markdown', 'packet', 'document') ?? (typeof full?.json === 'string' ? full.json : '');
    const size = pick(full?.json, 'bytes', 'size', 'sizeBytes');
    return h('div', { class: 'col', style: { gap: '14px' } },
      card({
        title: 'Handoff packet',
        sub: `bounded export for another person or agent${size ? ` · ${bytes(Number(size))}` : ''}`,
        iconName: 'download',
        actions: [
          button('Save markdown…', { size: 'sm', iconName: 'download', onClick: () => openCommandRunner('handoff', { values: { taskId: ctx.taskId, o: '' }, workspace: ctx.workspace }) }),
          copyButton(() => (typeof markdownText === 'string' ? markdownText : JSON.stringify(full?.json ?? {}, null, 2)), 'Copy packet'),
        ],
      },
        typeof markdownText === 'string' && markdownText.trim()
          ? markdown(markdownText)
          : jsonView(full?.json ?? {}, { label: 'handoff', depth: 2 })),
      card({ title: 'Compact packet', sub: 'tip, top claims, open items, next command — sized for an LLM context', iconName: 'note', flush: true },
        h('div', { class: 'card-body' },
          pick(compact?.json, 'markdown', 'packet')
            ? markdown(String(pick(compact.json, 'markdown', 'packet')))
            : jsonView(compact?.json ?? {}, { label: 'handoff --compact', depth: 2 }))),
      full && !full.ok ? alert({ tone: 'warn', title: 'handoff warning', body: full.error ?? '' }) : null,
    );
  },

  async files(ctx) {
    const listing = await api.store.files(ctx.taskId).catch(() => null);
    if (!listing?.found) {
      return alert({ tone: 'warn', title: 'Case directory not found in the central store', body: 'This session may live in a repo-local cases_dir. The CLI projections above still work — they locate sessions by id.' });
    }
    const viewer = h('div');
    const open = async (name) => {
      render(viewer, loading(`reading ${name}…`));
      const file = await api.store.file(ctx.taskId, name).catch(() => null);
      if (!file || file.error) { render(viewer, alert({ tone: 'error', title: 'Cannot read file', body: file?.error ?? 'unknown' })); return; }
      render(viewer, card({
        title: file.name,
        sub: `${bytes(file.size)} · modified ${relativeTime(file.mtime)}${file.truncated ? ' · truncated read' : ''}`,
        iconName: 'ledger',
        actions: [
          copyButton(() => file.text, 'Copy file'),
          button('Reveal', { size: 'sm', iconName: 'folder', onClick: () => api.shell.reveal(file.path) }),
        ],
        flush: true,
      },
        h('div', { class: 'card-body' },
          file.json ? jsonView(file.json, { label: file.name, depth: 2 })
            : file.records ? h('div', { class: 'col', style: { gap: '8px' } },
                badge(`${file.records.records.length} records`, 'info'),
                file.records.corrupt.length ? alert({ tone: 'warn', title: `${file.records.corrupt.length} corrupt lines`, body: file.records.corrupt.map((c) => `line ${c.line}: ${c.error}`).join('\n') }) : null,
                timelineFeed(file.records.records.map((r) => r.value), { limit: 300 }),
              )
              : file.name.endsWith('.md') ? markdown(file.text)
                : h('pre', { class: 'code', text: truncate(file.text, 200_000) }),
        ),
      ));
    };

    const files = listing.files.filter((f) => !f.isDir);
    return h('div', { class: 'browser-grid' },
      card({ title: 'Case directory', sub: compactHome(listing.found.dir), iconName: 'folder', flush: true, actions: [button('Reveal', { size: 'sm', iconName: 'folder', onClick: () => api.shell.reveal(listing.found.dir) })] },
        h('div', { class: 'file-list' }, files.map((file) =>
          h('button', { class: 'file-item', onclick: () => open(file.name) },
            icon(file.name.endsWith('.jsonl') ? 'ledger' : file.name.endsWith('.md') ? 'note' : 'file', 12),
            h('span', { class: 'grow', text: file.name, title: file.meta?.summary ?? file.name }),
            h('span', { class: 'file-size', text: bytes(file.size) }),
          ),
        ))),
      h('div', { ref: (node) => { node.appendChild(viewer); } },
        h('div', { class: 'col', style: { gap: '10px' } },
          card({ title: 'What each file is', sub: 'docs/case-file.md — the kernel’s working memory, not a transcript', iconName: 'book' },
            h('div', { class: 'list' }, (listing.files.filter((f) => f.meta)).map((file) =>
              h('div', { class: 'list-item' },
                h('span', { class: 'mono-sm accent-text', style: { minWidth: '132px' }, text: file.name }),
                h('span', { class: 'grow tiny faint', text: file.meta.summary }),
                h('span', { class: 'file-size', text: bytes(file.size) }),
              ),
            ))),
        ),
      ),
    );
  },

  async actions(ctx) {
    const grouped = [
      { label: 'Advance the loop', ids: ['investigate', 'plan', 'begin-change', 'verify', 'remember'] },
      { label: 'Change ownership', ids: ['lease.renew', 'lease.release'] },
      { label: 'Collaborate', ids: ['note', 'decision.request', 'decision.answer', 'decision.resume', 'handoff'] },
      { label: 'Hypotheses & evidence', ids: ['resolve', 'read-evidence', 'read-artifact'] },
      { label: 'Long-running work', ids: ['finding.add', 'finding.list', 'dossier.add', 'dossier.list', 'coverage', 'workplan.add', 'workplan.next', 'job.list', 'job.cancel'] },
      { label: 'Lifecycle', ids: ['status', 'show', 'timeline', 'metrics', 'resume', 'abort', 'archive', 'unarchive', 'rm'] },
    ];
    return h('div', { class: 'col', style: { gap: '14px' } },
      ctx.status?.actions?.length
        ? card({ title: 'Kernel-recommended next actions', sub: 'from status --json', iconName: 'arrowRight' }, actionsBoard(ctx))
        : null,
      ...grouped.map((group) => card({ title: group.label, iconName: 'bolt', flush: true },
        h('div', { class: 'card-body task-grid' }, group.ids
          .map((id) => state.commands.get(id))
          .filter(Boolean)
          .map((command) => h('button', {
            class: 'feature-card',
            onclick: () => openCommandRunner(command.id, { values: { taskId: ctx.taskId }, workspace: ctx.workspace }),
          },
            h('div', { class: 'feature-title' }, icon(command.kind === 'read' ? 'eye' : command.kind === 'destructive' ? 'trash' : 'bolt', 13), h('span', { text: command.title })),
            h('div', { class: 'feature-summary', text: truncate(command.summary, 150) }),
            h('div', { class: 'feature-foot' }, badge(`cortex ${command.path.join(' ')}`, 'slate'), command.kind === 'destructive' ? badge('destructive', 'red') : null),
          ))),
      )),
    );
  },
};

function actionsBoard(ctx) {
  const actions = firstArray(ctx.status, 'actions') || firstArray(ctx.show, 'actions');
  if (!actions.length) return empty({ iconName: 'arrowRight', title: 'No structured actions', note: 'Write commands return the next safe continuation; read commands do not.' });
  return h('div', { class: 'action-list' }, actions.map((action) =>
    h('button', { class: 'action-item', onclick: () => followAction(action) },
      h('span', { class: 'action-tool', text: String(pick(action, 'tool') ?? 'action') }),
      h('span', { class: 'action-reason', text: String(pick(action, 'reason') ?? '') }),
      h('span', { class: 'action-cmd', text: truncate(String(pick(action, 'command') ?? ''), 120) }),
    ),
  ));
}

function boundaryPanel(boundary) {
  const files = firstArray(boundary, 'files');
  const symbols = firstArray(boundary, 'symbols');
  if (!files.length && !symbols.length) {
    return alertNote('No boundary declared', 'Change tasks must declare one; the planning gate rejects plans without it.');
  }
  return h('div', { class: 'col', style: { gap: '8px' } },
    files.length
      ? h('div', {},
          h('div', { class: 'micro', text: 'declared files' }),
          h('div', { class: 'pill-row', style: { marginTop: '5px' } }, files.slice(0, 40).map((f) => h('span', { class: 'tag', text: truncate(flat(f), 44) }))))
      : null,
    symbols.length
      ? h('div', {},
          h('div', { class: 'micro', text: 'declared symbols' }),
          h('div', { class: 'pill-row', style: { marginTop: '5px' } }, symbols.slice(0, 40).map((s) => h('span', { class: 'tag', text: truncate(flat(s), 44) }))))
      : null,
    boundary?.reason ? h('div', { class: 'tiny faint', text: truncate(flat(boundary.reason), 240) }) : null,
  );
}

async function receiptsHost(ctx) {
  const file = await api.store.file(ctx.taskId, 'verification.json').catch(() => null);
  const stored = file?.json ?? {};
  const receipts = firstArray(stored, 'receipts', 'batches', 'records', 'items', 'claims');
  return h('div', { class: 'card-body' },
    receipts.length
      ? receiptsList(receipts)
      : empty({ iconName: 'shield', title: 'No receipts yet', note: 'verify writes one receipt per typed claim; a claim with no relevant verifier is recorded not_run — never passed.' }));
}

function openResolve(ctx, hypothesis) {
  const id = pick(hypothesis, 'id', 'hypothesisId') ?? '';
  openCommandRunner('resolve', {
    values: { taskId: ctx.taskId, hypothesisId: String(id) },
    workspace: ctx.workspace,
    title: `Resolve hypothesis ${shortId(id, 12, 4)}`,
  });
}

async function answerDecision(ctx, decision, optionId) {
  const id = String(pick(decision, 'id', 'decisionId') ?? '');
  openCommandRunner('decision.answer', {
    values: { taskId: ctx.taskId, decisionId: id, answer: optionId },
    workspace: ctx.workspace,
    title: `Answer decision ${shortId(id, 10, 4)}`,
  });
}

function findingAction(ctx, action, finding) {
  const findingId = String(pick(finding, 'id', 'findingId') ?? '');
  const map = { triage: 'finding.triage', convert: 'finding.convert', dismiss: 'finding.dismiss' };
  openCommandRunner(map[action], { values: { taskId: ctx.taskId, findingId }, workspace: ctx.workspace });
}

function openEvidence(ctx, evidence) {
  const id = String(pick(evidence, 'id', 'evidenceId') ?? '');
  if (!id) return;
  openCommandRunner('read-evidence', { values: { taskId: ctx.taskId, evidenceId: id }, workspace: ctx.workspace, title: `Evidence ${shortId(id, 12, 4)}` });
}

function openArtifact(ctx, evidence) {
  const ref = flat(pick(evidence, 'rawRef', 'uri', 'ref'));
  if (!ref) return;
  openCommandRunner('read-artifact', {
    values: { taskId: ctx.taskId, ref },
    workspace: ctx.workspace,
    title: `Raw ${shortId(ref, 20, 6)}`,
  });
}

function countBy(list, keyFn) {
  const out = {};
  for (const item of list) {
    const key = keyFn(item);
    out[key] = (out[key] ?? 0) + 1;
  }
  return out;
}

/* ── Task picker (no case selected) ────────────────────────────────────── */

async function taskPicker() {
  const root = h('div', { class: 'col', style: { gap: '14px' } });
  render(root, loading('finding something to open…'));
  const [listResult, activeResult] = await Promise.all([
    api.run('list', {}, { stream: false }).catch(() => null),
    api.run('sessions', { active: true }, { stream: false }).catch(() => null),
  ]);
  const workspaceRows = firstArray(listResult?.json, 'tasks', 'items', 'sessions');
  const activeRows = firstArray(activeResult?.json, 'sessions', 'items', 'tasks');

  const pickList = (rows, title, sub) => card({ title, sub, iconName: 'case', flush: true },
    rows.length
      ? h('div', { class: 'file-list', style: { maxHeight: '38vh' } }, rows.slice(0, 60).map((row) => {
          const taskId = String(pick(row, 'taskId', 'id') ?? '');
          return h('button', {
            class: 'file-item',
            onclick: () => navigate('case', { taskId, repo: pick(row, 'repo', 'repository'), workspace: pick(row, 'workspace', 'workspacePath') ?? state.workspace }),
          },
            stateBadge(pick(row, 'phase', 'state') ?? ''),
            h('span', { class: 'grow truncate', text: truncate(String(pick(row, 'goal', 'title') ?? taskId), 110) }),
            h('span', { class: 'mono-sm faint', text: relativeTime(pick(row, 'updatedAt', 'updated', 'mtime') ?? '') }),
          );
        }))
      : empty({ title: 'Nothing here' }),
  );

  render(root,
    h('div', { class: 'page-head' },
      h('div', { class: 'grow' },
        h('h1', { class: 'page-title', text: 'Open a case' }),
        h('p', { class: 'page-sub', text: 'Pick a session from this workspace, an in-flight session from any repository, or start something new. Every case view exposes the full loop: investigate, plan, lease, verify, remember — plus evidence, decisions, findings, coverage, workplans, jobs, handoff, and the raw case files.' }),
      ),
      h('div', { class: 'page-actions' },
        button('New case', { kind: 'primary', iconName: 'plus', onClick: () => openCommandRunner('open') }),
        button('Browse all sessions', { iconName: 'sessions', onClick: () => navigate('sessions') }),
      ),
    ),
    h('div', { class: 'grid grid-2' },
      pickList(workspaceRows, 'This workspace', 'cortex list'),
      pickList(activeRows, 'In flight, every repository', 'cortex sessions --active'),
    ),
    h('div', { class: 'sticky-sub', style: { position: 'static', margin: '0', padding: '0', border: '0', background: 'transparent' } },
      h('div', { class: 'micro', text: 'or jump straight to a task id' }),
    ),
    quickIdJump(),
  );
  return root;
}

function quickIdJump() {
  const input = h('input', { class: 'input mono', placeholder: 'task_06FK…', style: { maxWidth: '420px' } });
  return h('div', { class: 'row', style: { gap: '8px' } },
    input,
    button('Open', {
      kind: 'primary',
      onClick: async () => {
        const value = input.value.trim();
        if (!value) return;
        // show/timeline locate centrally, but status/verify and friends need the
        // owning workspace — read it off the case snapshot before navigating.
        const located = await api.store.locate(value).catch(() => null);
        let workspace = state.workspace;
        if (located) {
          const caseFile = await api.store.file(value, 'case.json').catch(() => null);
          const root = caseFile?.json?.workspace?.root ?? caseFile?.json?.workspace;
          if (typeof root === 'string' && root) workspace = root;
        }
        navigate('case', { taskId: value, repo: located?.repo, workspace });
      },
    }),
    h('span', { class: 'tiny faint', text: 'show / timeline / handoff locate a session by id from any directory' }),
  );
}
