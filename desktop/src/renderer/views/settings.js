/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { api } from '../lib/api.js';
import { h, render } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { confirmDialog } from '../lib/modal.js';
import { navigate } from '../lib/router.js';
import { applyTheme, refreshEnvironment, setSettings, state } from '../lib/state.js';
import { alert, badge, button, card, kv, loading, pageHead, toast } from '../lib/ui.js';
import { basename, compactHome, truncate } from '../lib/format.js';

export const settingsRoute = {
  id: 'settings',
  label: 'Settings',
  icon: 'sliders',
  group: 'Reference',
  hint: 'binary, workspace, appearance, approvals',
  async render() {
    const root = h('div', { class: 'col', style: { gap: '14px' } });
    render(root, loading('reading settings…'));
    const settings = state.settings ?? (await api.settings.get());
    const info = state.appInfo ?? (await api.appInfo().catch(() => null));
    const probe = state.probe;

    const binaryInput = h('input', { class: 'input mono', placeholder: 'empty = auto-resolve', value: settings.binaryPath ?? '' });
    const actorInput = h('input', { class: 'input mono', placeholder: 'agent-auth', value: settings.defaultActor ?? '' });
    const probeHost = h('div', { class: 'row-wrap', style: { gap: '6px' } },
      probe?.ok ? badge(probe.version ?? 'ok', 'green') : badge(probe?.error ?? 'not probed', 'red'),
      h('span', { class: 'mono-sm faint', text: `${probe?.source ?? ''} ${compactHome(probe?.path ?? '')}` }),
    );

    const save = async (patch, label) => {
      await setSettings(patch);
      toast('ok', 'Settings saved', label ?? '', 2600);
    };

    const toggle = (key, label, description, danger = false) => {
      const id = `approval-${key}`;
      return h('div', { class: 'setting-row' },
        h('div', {},
          h('div', { class: 'setting-name', text: label }),
          h('div', { class: 'setting-desc', text: description }),
        ),
        h('div', { class: 'setting-control' },
          h('label', { class: 'switch', for: id },
            h('input', {
              type: 'checkbox',
              id,
              checked: Boolean(settings.approvals?.[key]),
              onchange: async (event) => {
                const next = event.target.checked;
                if (next && danger) {
                  const ok = await confirmDialog({
                    title: `Grant ${label}?`,
                    body: description,
                    details: `CORTEX_APPROVE_${key === 'commands' ? 'COMMANDS' : key === 'remoteRecall' ? 'REMOTE_RECALL' : 'TRAJECTORY'}=1 will be set for every cortex process this app launches.`,
                    danger: true,
                    confirmLabel: 'Grant it',
                  });
                  if (!ok) { event.target.checked = false; return; }
                }
                await save({ approvals: { [key]: next } }, `${label}: ${next ? 'granted' : 'revoked'}`);
              },
            }),
            h('span', { class: 'switch-track' }),
            h('span', { class: 'switch-text', text: settings.approvals?.[key] ? 'granted' : 'not granted' }),
          ),
        ),
      );
    };

    render(root,
      pageHead({
        title: 'Settings',
        sub: 'Stored in Electron’s userData directory, never in the repository. Everything here changes how the deck launches cortex — the kernel’s own configuration lives in cortex.yaml.',
        actions: [
          button('Environment', { iconName: 'pulse', onClick: () => navigate('environment') }),
          button('Reset all', {
            kind: 'danger',
            iconName: 'trash',
            onClick: async () => {
              const ok = await confirmDialog({ title: 'Reset deck settings?', body: 'Binary path, workspace, theme, approvals, and timeouts return to their defaults.', danger: true, confirmLabel: 'Reset' });
              if (!ok) return;
              await api.settings.reset();
              await refreshEnvironment();
              navigate('settings', {}, { force: true });
            },
          }),
        ],
      }),

      card({ title: 'Cortex binary', sub: 'resolution order: this setting → <workspace>/bin/cortex → <cwd>/bin/cortex → PATH', iconName: 'cpu' },
        h('div', { class: 'setting-row' },
          h('div', {}, h('div', { class: 'setting-name', text: 'Binary path' }), h('div', { class: 'setting-desc', text: 'Absolute path to a cortex executable. Leave empty to auto-resolve; task build produces ./bin/cortex.' })),
          h('div', { class: 'setting-control' },
            h('div', { class: 'row', style: { gap: '8px' } },
              binaryInput,
              button('Save', { onClick: async () => { await save({ binaryPath: binaryInput.value.trim() }, binaryInput.value.trim() || 'auto-resolve'); await refreshEnvironment(); navigate('settings', {}, { force: true }); } }),
            ),
            probeHost,
            h('div', { class: 'row', style: { gap: '8px', marginTop: '4px' } },
              button('Re-probe', { size: 'sm', iconName: 'refresh', onClick: async () => { await refreshEnvironment(); navigate('settings', {}, { force: true }); } }),
              button('Build ./bin/cortex', { size: 'sm', iconName: 'wrench', onClick: () => navigate('dev', { task: 'build' }) }),
            ),
          ),
        ),
      ),

      card({ title: 'Workspace', sub: 'the repository cortex runs against (-C)', iconName: 'workspace' },
        h('div', { class: 'setting-row' },
          h('div', {}, h('div', { class: 'setting-name', text: 'Current workspace' }), h('div', { class: 'setting-desc', text: compactHome(state.workspace) })),
          h('div', { class: 'setting-control' },
            h('div', { class: 'row-wrap', style: { gap: '6px' } },
              button('Choose folder…', { iconName: 'folder', onClick: async () => { const result = await api.workspace.choose(); if (!result?.canceled) { await refreshEnvironment(); navigate('settings', {}, { force: true }); } } }),
              button('Reveal', { iconName: 'external', onClick: () => api.shell.reveal(state.workspace) }),
              badge(basename(state.workspace) || 'none', 'accent'),
            ),
            settings.recentWorkspaces?.length
              ? h('div', { class: 'pill-row', style: { marginTop: '8px' } }, settings.recentWorkspaces.map((dir) =>
                  h('button', { class: 'btn btn-sm', title: dir, onclick: async () => { await save({ workspace: dir }, basename(dir)); await refreshEnvironment(); navigate('workspace', {}, { force: true }); } }, icon('folder', 11), basename(dir)),
                ))
              : null,
          ),
        ),
        h('div', { class: 'setting-row' },
          h('div', {}, h('div', { class: 'setting-name', text: 'Default actor' }), h('div', { class: 'setting-desc', text: 'Prefills --actor for begin-change, verify, notes, findings, and workplan claims.' })),
          h('div', { class: 'setting-control' },
            h('div', { class: 'row', style: { gap: '8px' } },
              actorInput,
              button('Save', { onClick: () => save({ defaultActor: actorInput.value.trim() }) }),
            ),
          ),
        ),
      ),

      card({ title: 'Trusted-launcher approvals', sub: 'repository configuration can never approve itself — only the process launching cortex can', iconName: 'shield' },
        alert({
          tone: 'warn',
          title: 'These are real grants',
          body: 'Cortex blocks configured command verifiers, non-loopback recall endpoints, and the empirical trajectory runner unless the launching environment sets the matching CORTEX_APPROVE_* variable. Toggling them here makes the deck that launcher for every cortex process it spawns.',
        }),
        toggle('commands', 'CORTEX_APPROVE_COMMANDS', 'Allow repository-configured command verifiers (arbitrary local argv declared under verifiers: in cortex.yaml) to execute during verify.', true),
        toggle('remoteRecall', 'CORTEX_APPROVE_REMOTE_RECALL', 'Allow a non-loopback recall.embed_url for cross-case recall embeddings. Loopback endpoints never need this.', true),
        toggle('trajectory', 'CORTEX_APPROVE_TRAJECTORY', 'Allow the opt-in empirical trajectory harness (task trajectory) to run a trusted launcher and oracle argv.', true),
      ),

      card({ title: 'Appearance & behavior', iconName: 'sliders' },
        h('div', { class: 'setting-row' },
          h('div', {}, h('div', { class: 'setting-name', text: 'Theme' }), h('div', { class: 'setting-desc', text: 'Dark is the default; light is a separate palette, not an inversion.' })),
          h('div', { class: 'setting-control' },
            h('div', { class: 'btn-group' }, ['dark', 'light'].map((value) =>
              h('button', {
                class: 'btn',
                dataset: { active: String((settings.theme ?? 'dark') === value) },
                onclick: async () => { await save({ theme: value }); applyTheme(); navigate('settings', {}, { force: true }); },
              }, value)),
            ),
          ),
        ),
        h('div', { class: 'setting-row' },
          h('div', {}, h('div', { class: 'setting-name', text: 'Density' }), h('div', { class: 'setting-desc', text: 'Compact tightens row height and padding for dense ledgers.' })),
          h('div', { class: 'setting-control' },
            h('div', { class: 'btn-group' }, ['comfortable', 'compact'].map((value) =>
              h('button', {
                class: 'btn',
                dataset: { active: String((settings.density ?? 'comfortable') === value) },
                onclick: async () => { await save({ density: value }); applyTheme(); navigate('settings', {}, { force: true }); },
              }, value)),
            ),
          ),
        ),
        h('div', { class: 'setting-row' },
          h('div', {}, h('div', { class: 'setting-name', text: 'Command timeout' }), h('div', { class: 'setting-desc', text: 'Read commands use half of this; writes use the long timeout below.' })),
          h('div', { class: 'setting-control' },
            h('div', { class: 'row', style: { gap: '8px' } },
              numberInput(settings.timeouts?.defaultMs ?? 120000, 5000, 600000, 5000, (value) => save({ timeouts: { defaultMs: value } }, `${value}ms`)),
              h('span', { class: 'tiny faint', text: 'ms (read commands)' }),
            ),
            h('div', { class: 'row', style: { gap: '8px', marginTop: '6px' } },
              numberInput(settings.timeouts?.longMs ?? 600000, 30000, 3600000, 30000, (value) => save({ timeouts: { longMs: value } }, `${value}ms`)),
              h('span', { class: 'tiny faint', text: 'ms (writes, reviews, investigations)' }),
            ),
          ),
        ),
      ),

      card({ title: 'Keyboard', sub: 'the deck is operable without the mouse', iconName: 'bolt' },
        h('div', { class: 'list' },
          shortcut('⌘K', 'Command palette — every command, view, and session'),
          shortcut('⌘1 … ⌘9', 'Jump between the primary views'),
          shortcut('⌘R', 'Re-run the current view’s reads'),
          shortcut('⌘,', 'Settings'),
          shortcut('Esc', 'Close the topmost dialog'),
        ),
      ),

      card({ title: 'About', iconName: 'info' },
        kv([
          ['App', `${info?.name ?? 'Cortex Deck'} ${info?.version ?? ''}`],
          ['Electron', info?.electron ?? '—'],
          ['Chromium', info?.chrome ?? '—'],
          ['Node', info?.node ?? '—'],
          ['Platform', `${info?.platform ?? ''} ${info?.arch ?? ''}`],
          ['Settings file', h('span', { class: 'mono-sm', text: compactHome(info?.settingsFile ?? '') })],
          ['User data', h('span', { class: 'mono-sm', text: compactHome(info?.userData ?? '') })],
          ['Cortex', probe?.ok ? badge(truncate(probe.version ?? 'ok', 40), 'green') : badge('not found', 'red')],
        ]),
      ),
    );
    return root;
  },
};

function numberInput(value, min, max, step, onCommit) {
  const input = h('input', { class: 'input', type: 'number', min: String(min), max: String(max), step: String(step), style: { maxWidth: '140px' } });
  input.value = String(value);
  input.onchange = () => {
    const next = Number(input.value);
    if (Number.isFinite(next) && next >= min && next <= max) onCommit(next);
    else input.value = String(value);
  };
  return input;
}

function shortcut(keys, description) {
  return h('div', { class: 'list-item' },
    h('kbd', { text: keys }),
    h('span', { class: 'grow tiny', text: description }),
  );
}
