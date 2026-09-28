/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { api } from '../lib/api.js';
import { h, render } from '../lib/dom.js';
import { openCommandRunner } from '../lib/forms.js';
import { jsonView } from '../lib/jsonview.js';
import { navigate } from '../lib/router.js';
import { alert, badge, button, card, empty, loading, pageHead, stateBadge, table } from '../lib/ui.js';
import { firstArray, pick, receiptsList, sessionRow } from '../lib/renderers.js';
import { relativeTime, shortId, sortBy, truncate } from '../lib/format.js';

const SURFACES = ['code', 'browser', 'terminal'];
const RISKS = ['low', 'medium', 'high'];

export const reviewRoute = {
  id: 'review',
  label: 'Review',
  icon: 'branch',
  group: 'Work',
  hint: 'evidence-backed review of a branch or pull request',
  async render({ params }) {
    const root = h('div', { class: 'col', style: { gap: '14px' } });
    render(root, loading('reading the working tree…'));

    const [git, reviewCases] = await Promise.all([
      api.repo.git().catch(() => null),
      api.run('sessions', { repo: '', query: 'review' }, { stream: false, timeoutMs: 60_000 }).catch(() => null),
    ]);

    const values = { base: params?.base ?? '', head: params?.head ?? '', pr: params?.pr ?? '', risk: 'medium', surface: ['code'], claim: [] };
    const resultHost = h('div', { class: 'col', style: { gap: '12px' } });

    const base = h('input', { class: 'input mono', placeholder: 'merge-base with the default branch', value: values.base, oninput: (e) => { values.base = e.target.value; } });
    const head = h('input', { class: 'input mono', placeholder: 'current branch', value: values.head, oninput: (e) => { values.head = e.target.value; } });
    const pr = h('input', { class: 'input mono', placeholder: 'PR number (GitHub or Bitbucket)', type: 'number', oninput: (e) => { values.pr = e.target.value; } });
    const risk = h('select', { class: 'select', onchange: (e) => { values.risk = e.target.value; } }, RISKS.map((r) => h('option', { value: r, text: r, selected: r === values.risk })));

    const surfaces = SURFACES.map((surface) => {
      const id = `surface-${surface}`;
      return h('label', { class: 'switch', for: id },
        h('input', {
          type: 'checkbox',
          id,
          checked: values.surface.includes(surface),
          onchange: (event) => {
            values.surface = event.target.checked ? [...values.surface, surface] : values.surface.filter((s) => s !== surface);
          },
        }),
        h('span', { class: 'switch-track' }),
        h('span', { class: 'switch-text', text: surface }),
      );
    });

    const claims = [];
    const claimChips = h('div', { class: 'row-wrap', style: { gap: '5px' } });
    const claimInput = h('input', {
      class: 'input',
      placeholder: 'an extra claim to prove + enter',
      onkeydown: (event) => {
        if (event.key !== 'Enter') return;
        const value = event.target.value.trim();
        if (!value) return;
        claims.push(value);
        event.target.value = '';
        paintClaims();
      },
    });
    const paintClaims = () => {
      values.claim = [...claims];
      render(claimChips, claims.map((claim, index) =>
        h('span', { class: 'token' },
          h('span', { text: truncate(claim, 70) }),
          h('button', { onclick: () => { claims.splice(index, 1); paintClaims(); } }, '×'),
        ),
      ));
    };

    const runButton = button('Run review', { kind: 'primary', iconName: 'branch' });
    runButton.onclick = async () => {
      runButton.disabled = true;
      render(resultHost, loading('reviewing the diff — this runs codemap and any covering specs…'));
      try {
        const payload = { risk: values.risk };
        if (values.base) payload.base = values.base;
        if (values.head) payload.head = values.head;
        if (values.pr) payload.pr = Number(values.pr);
        if (values.surface.length) payload.surface = values.surface;
        if (claims.length) payload.claim = claims;
        const result = await api.run('review', payload, { stream: true, timeoutMs: 600_000 });
        render(resultHost, reviewResult(result));
      } catch (err) {
        render(resultHost, alert({ tone: 'error', title: 'Review failed', body: String(err?.message ?? err) }));
      } finally {
        runButton.disabled = false;
      }
    };

    const past = sortBy(firstArray(reviewCases?.json, 'sessions', 'items', 'tasks').map(sessionRow), (r) => r.updated || '').slice(0, 12);

    render(root,
      pageHead({
        title: 'Review',
        sub: 'Resolve the diff (base…HEAD), gather structural and semantic context, run the verifiers over the change, and produce a verdict — approve / request-changes / needs-verification — where every claim is backed by a receipt.',
        actions: [
          git?.ok ? badge(`${git.branch} @ ${git.head}`, 'accent') : badge('no git', 'red'),
          git?.dirty ? badge(`${git.dirty} uncommitted`, 'amber') : null,
          button('Docs', { iconName: 'book', onClick: () => navigate('repo', { tab: 'docs', path: 'docs/cli.md' }) }),
        ],
      }),
      card({
        title: 'Review scope',
        sub: 'a pull request is fetched host-agnostically by git ref; when a host cannot be fetched by ref, check the branch out and use --base',
        iconName: 'branch',
        actions: [runButton, button('Full runner…', { size: 'sm', onClick: () => openCommandRunner('review') })],
      },
        h('div', { class: 'form-grid' },
          h('div', { class: 'field' }, h('label', { class: 'field-label' }, 'Base ref'), base, h('div', { class: 'field-help', text: 'default: merge-base with the default branch' })),
          h('div', { class: 'field' }, h('label', { class: 'field-label' }, 'Head ref'), head, h('div', { class: 'field-help', text: 'default: current branch' })),
          h('div', { class: 'field' }, h('label', { class: 'field-label' }, 'PR number'), pr),
          h('div', { class: 'field' }, h('label', { class: 'field-label' }, 'Risk band'), risk),
          h('div', { class: 'field span-2' }, h('label', { class: 'field-label' }, 'Surfaces'), h('div', { class: 'row-wrap', style: { gap: '14px' } }, surfaces), h('div', { class: 'field-help', text: 'browser and terminal also auto-run the specs that cover the change' })),
          h('div', { class: 'field span-2' }, h('label', { class: 'field-label' }, 'Additional claims'), h('div', { class: 'tokens' }, claimChips, claimInput)),
        ),
        git?.changes?.length
          ? h('div', { style: { marginTop: '12px' } },
              h('div', { class: 'micro', text: `${git.changes.length} uncommitted change${git.changes.length === 1 ? '' : 's'} in the working tree` }),
              h('div', { class: 'pill-row', style: { marginTop: '6px' } }, git.changes.slice(0, 30).map((c) => h('span', { class: 'tag', title: c.code, text: truncate(c.path, 40) }))),
            )
          : null,
      ),
      resultHost,
      card({ title: 'Past review cases', sub: 'sessions whose goal mentions review', iconName: 'history', flush: true },
        past.length
          ? table({
              rows: past,
              maxHeight: 280,
              rowKey: (row) => row.taskId,
              onRowClick: (row) => navigate('case', { taskId: row.taskId, workspace: row.workspace }),
              columns: [
                { key: 'phase', label: 'Phase', width: '120px', render: (row) => stateBadge(row.phase) },
                { key: 'goal', label: 'Goal', strong: true, render: (row) => truncate(row.goal, 120) },
                { key: 'assessment', label: 'Assessment', width: '112px', render: (row) => (row.assessment ? stateBadge(row.assessment) : '—') },
                { key: 'updated', label: 'Updated', width: '106px', align: 'right', render: (row) => h('span', { class: 'mono-sm faint', text: relativeTime(row.updated) }) },
              ],
            })
          : empty({ iconName: 'history', title: 'No previous review cases', note: 'Run a review above — it opens its own case, so every claim keeps a receipt you can inspect.' })),
    );
    return root;
  },
};

function reviewResult(result) {
  if (!result) return null;
  const json = result.json ?? {};
  const verdict = String(pick(json, 'verdict', 'decision', 'assessment') ?? '').toLowerCase();
  const tone = verdict.includes('approve') ? 'ok' : verdict.includes('request') ? 'error' : verdict ? 'warn' : 'info';
  const receipts = firstArray(json, 'receipts', 'verification', 'claims');
  const findings = firstArray(json, 'findings', 'issues', 'comments');
  const diff = pick(json, 'diff', 'changedFiles', 'files');
  const taskId = pick(json, 'taskId', 'task');

  return h('div', { class: 'col', style: { gap: '12px' } },
    alert({
      tone: result.ok ? tone : 'error',
      title: verdict ? `Verdict: ${verdict}` : result.ok ? 'Review complete' : 'Review failed',
      body: pick(json, 'summary') ?? result.error ?? result.summary ?? '',
    }),
    taskId
      ? h('div', { class: 'row-wrap' },
          badge(`case ${shortId(String(taskId), 16, 4)}`, 'accent'),
          button('Open the case', { size: 'sm', iconName: 'case', onClick: () => navigate('case', { taskId: String(taskId) }) }),
          button('Status --detail full', { size: 'sm', iconName: 'pulse', onClick: () => openCommandRunner('status', { values: { taskId: String(taskId), detail: 'full' } }) }),
        )
      : null,
    receipts.length ? card({ title: 'Receipts behind the verdict', sub: `${receipts.length} claims`, iconName: 'shield', flush: true }, h('div', { class: 'card-body' }, receiptsList(receipts))) : null,
    findings.length
      ? card({ title: 'Findings', iconName: 'sparkles', flush: true },
          h('div', { class: 'card-body col', style: { gap: '8px' } }, findings.map((finding) =>
            h('div', { class: 'evidence-item' },
              h('div', { class: 'evidence-claim', text: truncate(String(pick(finding, 'title', 'message', 'comment', 'text') ?? JSON.stringify(finding)), 320) }),
              h('div', { class: 'evidence-meta' },
                pick(finding, 'severity') ? stateBadge(String(pick(finding, 'severity'))) : null,
                pick(finding, 'path', 'file') ? h('span', { class: 'mono-sm', text: String(pick(finding, 'path', 'file')) }) : null,
                pick(finding, 'line') ? h('span', { class: 'mono-sm', text: `:${pick(finding, 'line')}` }) : null,
              ),
            ))))
      : null,
    diff
      ? card({ title: 'Diff scope', iconName: 'code', flush: true },
          h('div', { class: 'card-body' }, Array.isArray(diff)
            ? h('div', { class: 'pill-row' }, diff.slice(0, 80).map((f) => h('span', { class: 'tag', text: truncate(typeof f === 'string' ? f : String(pick(f, 'path', 'file') ?? JSON.stringify(f)), 48) })))
            : jsonView(diff, { label: 'diff', depth: 2 })))
      : null,
    card({ title: 'review --json', iconName: 'ledger', flush: true }, h('div', { class: 'card-body' }, jsonView(json, { label: 'review', depth: 1, collapsed: true }))),
  );
}
