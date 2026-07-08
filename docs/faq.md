# FAQ

Short answers to the questions that come up most often. New here? Do the
[tutorial](/tutorial) first — several answers below assume you've seen the loop once.

[[toc]]

## Getting started

### What is Cortex, in one sentence?

A local-first **agent kernel**: it sits between a language model and your specialist tools and
gives long tool-using tasks the things models are bad at holding onto — stable state, explicit
evidence, a disciplined tool-selection policy, bounded changes, verification tied to what a human
actually sees, and durable memory.

### Why not just let the agent call codemap / vecgrep / grep directly?

Because more raw tools without structure is *more ways to get lost*. An unsupervised agent will
"find" a cause with one search, confidently edit the wrong file, declare success, and leave no
trail. Cortex replaces "dozens of overlapping tools" with **six cognitive actions** and enforces an
order between them, so the model spends its intelligence on the problem instead of on bookkeeping it
routinely gets wrong. See [Concepts](/concepts) for the full argument.

### Do I have to install codemap, vecgrep, cairntrace, and the rest?

No. They're **all optional at runtime.** Run `cortex doctor` to see what's present. A missing tool
degrades gracefully: its adapter reports `tool_unavailable` and the loop continues — it never
fabricates output. You get more evidence with more tools installed, but the discipline (phases,
gates, receipts, boundaries) works with zero of them.

### How do I install it?

```bash
go install github.com/abdul-hamid-achik/cortex/cmd/cortex@latest
# or from a clone:
task build      # → ./bin/cortex
task install    # → $GOPATH/bin/cortex
```

It's a single pure-Go binary (`CGO_ENABLED=0`), no runtime dependencies.

### Should I use the CLI or the MCP server?

Both are the same kernel; pick by who's driving:

- **CLI** — for humans and shell scripts. Add `--json` to any read command for machine output.
- **MCP server** (`cortex serve`) — for agent harnesses. The model calls `cortex_start_task`, `cortex_investigate`, etc. as tools. See [MCP](/mcp).

## Using it day to day

### I lost my task id. How do I get it back?

```bash
cortex list          # all tasks in this workspace, newest first
cortex list --json   # machine-readable
```

Everything hangs off the `taskId`, so it's worth stashing in a shell variable while you work
(`TID=task_…`).

### What's a "surface"?

Where a change is *user-visible*: `code`, `browser`, `terminal`, `artifact`, or `secret`. You
declare surfaces at `start` (`--surface browser`), and they drive verification — a claim about
browser behavior needs a browser verifier (cairntrace), a terminal claim needs a terminal verifier
(glyphrun). Surfaces are how Cortex knows *what kind of proof a claim requires.*

### What's the difference between `not_run` and `passed`?

This is the heart of Cortex. A claim you make at `verify` gets a **receipt**. If the verifier that
could prove it never ran (tool missing, no spec provided), the receipt is `not_run` — **never**
`passed`. Cortex will not round an unchecked claim up to success. A task can't even complete without
at least one real receipt, unless you *explicitly* mark the whole outcome `--unverified`.

### What is "scope drift"?

At `plan` you declare a **change boundary** (`--file` / `--symbol`). At `verify`, Cortex diffs your
actual edits against that boundary. Touch a file you didn't declare and it reports `drifted` and
names the file. It doesn't stop you — it makes accidental scope expansion *visible* instead of
silent. Expand the plan deliberately if the scope really did change.

### Why did `plan` reject my hypothesis?

Because it had no **disproof path.** Every hypothesis must come with how it could be proven wrong —
use the `statement :: disproof` shorthand or paired `--hypothesis` / `--disprove` flags. A claim you
can't imagine falsifying is a belief, not a hypothesis, and the gate refuses it. A change-mode task
with no boundary is rejected for the same reason: undisciplined edits.

### Why won't `remember` complete my task?

Completion requires a verification receipt. Either provide a verifier and re-run `verify` so a real
receipt exists, or — if verification genuinely couldn't run — acknowledge that honestly with
`--unverified`. That flag isn't a shortcut; it permanently labels the outcome as *not* verified so
it can never masquerade as verified later.

### My high-risk change threw an extra warning at verify. Bug?

No — that's `--risk high` working as intended. High-risk changes must clear a stricter bar
(SPEC §13.3): a structural diff review that actually *passed*. If codemap isn't indexed the review
is inconclusive, and Cortex says so rather than waving the change through. Index codemap and
re-verify, or lower the risk band if it was overstated.

### Can I investigate more than once?

Yes. Each round is counted against a small budget (you'll see `rounds 2/3` in `status`). Exceeding
it doesn't hard-block you — it *warns*, so aimless searching becomes visible. The nudge is toward
forming a hypothesis and planning, not toward endless search.

### Is there a UI?

Yes — a read-only terminal UI over the case store:

```bash
cortex studio     # aliases: cortex board, cortex tui
```

It lists the workspace's tasks with their phase and shows the selected case's goal and evidence.
Navigate with arrow/`j`-`k` keys, quit with `q`.

## State, privacy, and secrets

### Where does Cortex store state?

Per-workspace, on disk, append-only, under `.agent/cases/<taskId>/` (case, evidence, hypotheses,
plan, receipts, command audit trail, raw tool output, and a generated `summary.md`). Global config
lives under `$CORTEX_HOME` (default `~/.cortex`). See [The case file](/case-file). Cortex also
writes `.agent/.gitignore` so its working state doesn't accidentally get committed.

### Does Cortex send my code anywhere?

The kernel itself is local — it orchestrates local tools and writes local files. What leaves your
machine is whatever the *model* and the *tools you configured* send (your LLM harness, or a tool
that calls a remote service). Cortex adds no network calls of its own.

### How does the secret-safety story work?

`tvault` is treated as an **execution boundary, not a content provider.** Secret *values* never
enter the model's context or an evidence record — the tvault adapter answers only availability and
capability questions ("is `STRIPE_KEY` present?"), never "what is it?". On top of that, a
**redactor** is the last-line filter on everything model-visible: known secret shapes and any
literals you list are masked before output. This is defense in depth — the adapter won't emit a
secret, and the redactor would catch it if anything tried to.

### Can I add my own strings to always redact?

Yes. In `cortex.yaml`:

```yaml
redact_literals:
  - MY_INTERNAL_HOSTNAME
  - some-project-codename
```

or via `CORTEX_REDACT_LITERALS=a,b,c`. **Never put secret *values* here** — list the *names* or
patterns you want masked, not the secrets themselves.

## Configuration & harnesses

### How do I change budgets or the case-file location?

A `cortex.yaml` at the repo root (or `.config/cortex.yaml`, or `$CORTEX_HOME/config.yaml`), plus
`CORTEX_*` env overrides. Precedence, low → high: defaults → global → project `.config` → project
root → env. See [Configuration](/configuration). Check what resolved with:

```bash
cortex config
```

### Which agent harnesses does Cortex work with?

Anything that speaks MCP. Register the server once with mcphub and every harness behind the gateway
sees it — this project is wired for **codex, claude, omp, opencode, hermes, and copilot.** `omp`
(oh-my-phi) inherits claude's config, so it needs no separate setup. Register with:

```bash
mcphub add cortex cortex serve
mcphub sync --write
```

In gateway mode the tools are namespaced `cortex__<tool>`. Details and recommended tool pins are in
[MCP](/mcp).

### Can I investigate a bug from a screen recording?

Yes, if you have [vidtrace](/adapters) installed. Pass a bundle path or a `vidtrace://` stash id:

```bash
cortex investigate $TID "the checkout button does nothing" --video vidtrace://vt_abc123
```

Cortex runs vidtrace to turn the recording into timestamped evidence and links the visible failure
to the code that owns it, then continues the normal discovery → structure route.

### Does it work outside a git repository?

Yes, but degraded. Without git there's no baseline commit, so scope-drift detection and diff review
are limited — `start` warns you. For real change work, run it inside a repo.

## Troubleshooting

### A tool shows as unavailable in `cortex doctor`.

That's informational, not an error — the tool isn't installed or isn't on `PATH`. Install it if you
want its evidence; otherwise the loop proceeds and that surface's verification is simply skipped
(recorded `not_run`, never `passed`).

### The output looks garbled / full of escape codes when I pipe it.

It shouldn't — Cortex detects a non-terminal and emits plain text when piped, styled only at a real
terminal. If you see raw ANSI in a pipe, please file it; add `--json` in the meantime for clean
machine output.

### How do I start over on a task?

Tasks are append-only by design (that's the audit trail). To abandon one without deleting its
evidence: `cortex abort <taskId>`. To wipe local state entirely, remove the workspace's `.agent/`
directory — but note you'll lose the reconstructable history of every task in it.

### Still stuck?

- [Concepts](/concepts) — the model behind the gates.
- [CLI reference](/cli) — every command and flag.
- [GitHub issues](https://github.com/abdul-hamid-achik/cortex) — bugs and questions.
