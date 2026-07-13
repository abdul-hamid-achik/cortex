# Studio: the human operator surface

Cortex Studio is a live, read-only terminal board for people supervising agent work. It reads the
same case files as the CLI and MCP server, so the view does not create a second source of truth or
change a case.

```bash
cortex studio
```

Studio is interactive and rejects `--json`; use `cortex sessions --json` for the board index or
`cortex show <taskId> --json` for one machine-readable session.

On wide terminals, the left pane lists sessions and the right pane shows the selected case. On
narrow terminals, the same panes stack at full width. Each list row includes a textual phase label,
so status remains readable without color. The detail pane uses the canonical session projection:
goal and loop position, `verified | partial | failed | unverified` assessment and gaps, a pending
human decision with option consequences, the first structured next action, hypotheses, bounded
recent receipts, and bounded recent evidence. The displayed projection retains at most the 200
newest receipts and combined activity entries; its composite store read also streams only the 200
newest evidence, command, and phase records while reporting exact totals. Studio refreshes the
central session store every two seconds; index and detail reads run in the background, and refresh
bursts are coalesced so repository scans do not block keyboard navigation. If a refresh fails,
Studio keeps the matching last good projection visible and reports that the snapshot may be stale.

## Filter the board

```bash
cortex studio --active       # start with in-flight sessions only
cortex studio --repo billing # match a repository name or slug
cortex studio --query "billing partial" # AND-search session identity, state, and outcome
```

Press `/` to edit the search without leaving the board, `Enter` to apply it, `Esc` to cancel, and
`c` to clear it. Search is case-insensitive and whitespace-separated terms are ANDed across task
ID, goal, phase, mode, repository/slug, workspace, and verification outcome—the same contract as
`cortex sessions --query` and the all-profile MCP sessions tool. While the background read is in
flight, Studio labels the requested filter separately from the last successfully applied filter;
a failed search never relabels retained rows as if they matched.

Inside Studio:

| Key | Action |
|---|---|
| `↑` / `k`, `↓` / `j` | select the previous or next session |
| `g`, `G` | jump to the first or last session |
| `Page Up` / `Page Down`, `Ctrl-U` / `Ctrl-D` | scroll the selected case detail |
| `/`, then `Enter` | edit and apply session search |
| `Esc` | cancel search editing |
| `c` | clear the applied search |
| `a` | toggle active-only sessions |
| `r` | refresh immediately |
| `q`, `Esc`, `Ctrl-C` | quit |

Studio flags an in-flight session after 24 hours without an update. Use the CLI when you need more
detail:

```bash
cortex show <taskId>          # bounded one-screen session view with exact totals
cortex timeline <taskId>      # time-sorted phases, evidence, calls, and receipts
cortex status <taskId>        # current blockers and missing verification in its workspace
cortex sessions --stale       # only stale in-flight sessions
```

## Which sessions appear?

Studio reads the central XDG store at `$XDG_STATE_HOME/cortex/sessions/`, the default case-file
location. A project configured with a repo-local `cases_dir` is intentionally outside this global
walk; use `cortex list` from that workspace instead. Run `cortex config` to see the resolved paths.

## The three surfaces

- **CLI** — direct operation, inspection, and shell automation.
- **MCP server** — the compact tool interface an agent calls.
- **Studio** — the read-only operator view for humans.

All three call the same kernel and read the same evidence model.

The MCP server's exposure profile does not change Studio. `cortex serve` defaults to the compact
17-tool `agent` profile, while Studio and the CLI retain the operator views locally. Use
`cortex serve --profile all` only when an MCP client also needs the 24-tool surface, including the
seven cross-repository monitoring and session-administration tools.
