/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { api } from '../lib/api.js';
import { h, render } from '../lib/dom.js';
import { openCommandRunner } from '../lib/forms.js';
import { navigate } from '../lib/router.js';
import { state } from '../lib/state.js';
import { alert, badge, button, card, empty, loading, pageHead } from '../lib/ui.js';
import { coverageView, dossierTable, findingsTable, firstArray, jobsTable, pick, workplanView } from '../lib/renderers.js';
import { shortId, sortBy, truncate } from '../lib/format.js';

export const longRunRoute = {
  id: 'longrun',
  label: 'Long-running',
  icon: 'layers',
  group: 'Work',
  hint: 'survey coverage, findings, dossier, campaigns, jobs',
  async render({ params }) {
    const root = h('div', { class: 'col', style: { gap: '14px' } });
    render(root, loading('gathering long-running work…'));

    const [sessionsResult, dossierResult] = await Promise.all([
      api.run('sessions', { active: true, repo: params?.repo ?? '' }, { stream: false }).catch(() => null),
      api.run('dossier.list', {}, { stream: false }).catch(() => null),
    ]);

    const candidates = sortBy(
      firstArray(sessionsResult?.json, 'sessions', 'items', 'tasks'),
      (s) => pick(s, 'updatedAt', 'updated', 'mtime') ?? '',
    );
    const selectedId = params?.taskId ?? state.selectedTask?.taskId ?? pick(candidates[0], 'taskId', 'id') ?? '';
    const dossierRows = firstArray(dossierResult?.json, 'entries', 'items') || arrayOfObjects(dossierResult?.json);

    const caseHost = h('div', { class: 'col', style: { gap: '14px', minWidth: '0' } });

    const selector = h('select', {
      class: 'select',
      style: { minWidth: '320px' },
      onchange: (event) => {
        const session = candidates.find((candidate) => String(pick(candidate, 'taskId', 'id') ?? '') === event.target.value);
        navigate('longrun', {
          taskId: event.target.value,
          repo: params?.repo,
          workspace: pick(session, 'workspace', 'workspacePath') ?? '',
        });
      },
    },
      h('option', { value: '', text: '— pick a case —', selected: !selectedId }),
      ...candidates.map((session) => {
        const id = String(pick(session, 'taskId', 'id') ?? '');
        return h('option', {
          value: id,
          selected: id === selectedId,
          text: `${shortId(id, 14, 4)} · ${truncate(String(pick(session, 'goal', 'title') ?? ''), 52)}`,
        });
      }),
    );

    render(root,
      pageHead({
        title: 'Long-running work',
        sub: 'Whole-repository comprehension, bug hunts, and multi-session campaigns: a survey coverage ledger, a per-repository dossier, a findings backlog, dependency-aware work plans, and detached jobs. See docs/long-running.md for the model.',
        actions: [
          selector,
          button('Docs', { iconName: 'book', onClick: () => navigate('repo', { tab: 'docs', path: 'docs/long-running.md' }) }),
          button('Survey case…', { kind: 'primary', iconName: 'layers', onClick: () => openCommandRunner('open', { values: { mode: 'survey' }, onDone: () => navigate('longrun', {}, { force: true }) }) }),
        ],
      }),
      card({
        title: 'Repository dossier',
        sub: `${dossierRows.length} entries · memory that outlives cases and goes stale when its files change`,
        iconName: 'database',
        actions: [
          button('Refresh freshness', { size: 'sm', iconName: 'refresh', onClick: () => openCommandRunner('dossier.refresh', {}, { onDone: () => navigate('longrun', params, { force: true }) }) }),
          button('Stale only', { size: 'sm', iconName: 'clock', onClick: () => openCommandRunner('dossier.list', { values: { stale: true } }) }),
          button('Add entry…', { size: 'sm', kind: 'primary', iconName: 'plus', onClick: () => openCommandRunner('dossier.add', { onDone: () => navigate('longrun', params, { force: true }) }) }),
        ],
        flush: true,
      }, h('div', { class: 'card-body' }, dossierTable(dossierRows, { maxHeight: 420 }))),
      caseHost,
    );

    if (!selectedId) {
      render(caseHost, card({ title: 'Case-scoped work', iconName: 'layers' },
        empty({
          iconName: 'layers',
          title: 'Pick a case above',
          note: 'Findings, coverage, workplans, and jobs are all scoped to one case. Choose an in-flight case — or open a survey case to build a coverage ledger for this repository.',
        })));
      return root;
    }

    render(caseHost, loading(`reading ${shortId(selectedId, 14, 4)}…`));
    // coverage/finding/workplan/job are workspace-scoped on the kernel side:
    // they only resolve from the case's owning repository, unlike show/timeline.
    const selectedSession = candidates.find((session) => String(pick(session, 'taskId', 'id') ?? '') === selectedId);
    const selectedWorkspace = pick(selectedSession, 'workspace', 'workspacePath') || state.workspace;
    const opts = { stream: false, workspace: selectedWorkspace };
    const [findings, coverage, workplan, jobs] = await Promise.all([
      api.run('finding.list', { taskId: selectedId }, opts).catch(() => null),
      api.run('coverage', { taskId: selectedId, all: true }, opts).catch(() => null),
      api.run('workplan.list', { taskId: selectedId }, opts).catch(() => null),
      api.run('job.list', { taskId: selectedId }, opts).catch(() => null),
    ]);

    const findingRows = firstArray(findings?.json, 'findings', 'items') || arrayOfObjects(findings?.json);
    const jobRows = firstArray(jobs?.json, 'jobs', 'items');
    const openFindings = findingRows.filter((f) => ['open', 'triaged'].includes(String(pick(f, 'status', 'state') ?? '').toLowerCase()));

    render(caseHost,
      h('div', { class: 'row-wrap' },
        badge(`case ${shortId(selectedId, 16, 4)}`, 'accent'),
        badge(`${findingRows.length} findings`, 'info'),
        openFindings.length ? badge(`${openFindings.length} need a decision`, 'amber') : null,
        badge(`${jobRows.length} jobs`, 'slate'),
        h('span', { class: 'grow' }),
        button('Open case', { size: 'sm', iconName: 'case', onClick: () => navigate('case', { taskId: selectedId }) }),
      ),
      card({
        title: 'Findings',
        sub: 'durable backlog of bugs, improvements, features, and questions backed by evidence',
        iconName: 'sparkles',
        actions: [button('Record finding…', { size: 'sm', kind: 'primary', iconName: 'plus', onClick: () => openCommandRunner('finding.add', { values: { taskId: selectedId }, onDone: () => navigate('longrun', params, { force: true }) }) })],
        flush: true,
      }, findingsTable(findingRows, {
        maxHeight: 380,
        onAction: (action, finding) => openCommandRunner(
          { triage: 'finding.triage', convert: 'finding.convert', dismiss: 'finding.dismiss' }[action],
          { values: { taskId: selectedId, findingId: String(pick(finding, 'id', 'findingId') ?? '') }, onDone: () => navigate('longrun', params, { force: true }) },
        ),
      })),
      card({
        title: 'Survey coverage',
        sub: 'which modules were explored, summarized, or never seen',
        iconName: 'layers',
        actions: [
          button('Investigate module…', { size: 'sm', iconName: 'search', onClick: () => openCommandRunner('investigate', { values: { taskId: selectedId }, onDone: () => navigate('longrun', params, { force: true }) }) }),
          button('Fan out…', { size: 'sm', iconName: 'bolt', onClick: () => openCommandRunner('investigate', { values: { taskId: selectedId, fanout: true }, onDone: () => navigate('longrun', params, { force: true }) }) }),
        ],
        flush: true,
      }, h('div', { class: 'card-body' }, coverage?.json ? coverageView(coverage.json) : alert({ tone: 'warn', title: 'No coverage ledger', body: coverage?.error ?? 'Only survey-mode cases build one.' }))),
      card({
        title: 'Campaign workplan',
        sub: 'dependency-aware items handed to actors as linked, retry-keyed child cases',
        iconName: 'jobs',
        actions: [
          button('Add item…', { size: 'sm', iconName: 'plus', onClick: () => openCommandRunner('workplan.add', { values: { taskId: selectedId }, onDone: () => navigate('longrun', params, { force: true }) }) }),
          button('Claim next…', { size: 'sm', iconName: 'arrowRight', onClick: () => openCommandRunner('workplan.next', { values: { taskId: selectedId }, onDone: () => navigate('longrun', params, { force: true }) }) }),
        ],
        flush: true,
      }, h('div', { class: 'card-body' }, workplan?.json ? workplanView(workplan.json, { onOpen: (id) => navigate('case', { taskId: id }) }) : empty({ title: 'No workplan', note: workplan?.error ?? 'Add an item to start a campaign.' }))),
      card({
        title: 'Background jobs',
        sub: 'detached investigations that outlive a single MCP call',
        iconName: 'bolt',
        actions: [button('Async investigate…', { size: 'sm', iconName: 'bolt', onClick: () => openCommandRunner('investigate', { values: { taskId: selectedId, async: true }, onDone: () => navigate('longrun', params, { force: true }) }) })],
        flush: true,
      }, h('div', { class: 'card-body' }, jobsTable(jobRows, {
        onCancel: (job) => openCommandRunner('job.cancel', {
          values: { taskId: selectedId, jobId: String(pick(job, 'id', 'jobId') ?? '') },
          onDone: () => navigate('longrun', params, { force: true }),
        }),
      }))),
    );
    return root;
  },
};

function arrayOfObjects(value) {
  return Array.isArray(value) ? value.filter((x) => x && typeof x === 'object') : [];
}
