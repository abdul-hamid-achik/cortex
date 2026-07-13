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
trail. Cortex replaces "dozens of overlapping tools" with a compact, state-aware workflow and
enforces its gates, so the model spends its intelligence on the problem instead of bookkeeping it
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

It's a single pure-Go Cortex binary (`CGO_ENABLED=0`), but **Git is required** for repository
identity, diffs, scope drift, and revision-bound verification. The specialist tools are optional.

### Should I use the CLI or the MCP server?

Both are the same kernel; pick by who's driving:

- **CLI** — for humans and shell scripts. Add `--json` to non-interactive read commands for machine
  output; Studio is interactive and directs machines to `sessions --json` / `show --json`.
- **MCP server** (`cortex serve`) — for agent harnesses. The model calls `cortex_open_task`,
  `cortex_investigate`, etc. as tools. See [MCP](/mcp).

### What's the difference between `open` and `start`?

`open` is retry-safe and should be the default for agents. An idempotency key returns the same case
even after completion; without a key, Cortex resumes the newest active case matching normalized
goal, mode, workspace, and branch. `start` always creates a fresh case, which is useful only when
duplication is deliberate.

## Using it day to day

### I lost my task id. How do I get it back?

```bash
cortex list          # all tasks in this workspace, newest first
cortex list --json   # machine-readable
```

Everything hangs off the `taskId`, so it's worth stashing in a shell variable while you work
(`TID=task_…`).

If you used `open --idempotency-key …`, repeating the open command is another safe way to recover
the exact ID.

### What's a "surface"?

Where a change is *user-visible*: `code`, `browser`, `terminal`, `artifact`, or `secret`. You
declare surfaces at `open`/`start` (`--surface browser`), and they drive verification — a claim about
browser behavior needs a browser verifier (cairntrace), a terminal claim needs a terminal verifier
(glyphrun). Surfaces are how Cortex knows *what kind of proof a claim requires.*

### What's the difference between `not_run` and `passed`?

This is the heart of Cortex. A claim you make at `verify` gets a **receipt**. If the verifier that
could prove it never ran (tool missing, no spec provided), the receipt is `not_run` — **never**
`passed`. Cortex will not round an unchecked claim up to success. All views reduce current receipts
to one assessment: `verified`, `partial`, `failed`, or `unverified`. Only `verified` is a normal
completion; explicit acknowledgments preserve the other outcomes honestly.

### Why do I need `begin-change` and an actor?

The boundary says *where* work may happen; the lease says *who currently owns it*. `begin-change`
atomically moves a planned change to `changing` and gives a stable, non-secret actor an expiring
lease (15 minutes by default). A same-owner retry is safe; another active actor is rejected. Pass
the same actor to `verify`, renew long work with `cortex lease renew`, and let `remember`/`abort`
release the lease.

### My task intentionally produced no diff. Is that an error?

By default, yes: a change task with no change record is usually an abandoned edit. If the no-op is
intentional, pass `cortex verify … --no-op` (MCP: `noOpAcknowledged: true`). This only acknowledges
the absence of a diff so verification can proceed. It does **not** create a passing receipt or make
the task verified.

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

Completion uses the canonical assessment. Common blockers:

- only `not_run` / `blocked` / `inconclusive` receipts → re-run `verify` with an available, indexed
  verifier, or use `--unverified` to preserve the explicitly unverified outcome
- canonical outcome is `failed` → fix the change and re-verify, or use `--accept-failed` to record
  the failed outcome explicitly (it will not be labeled as verified)
- some proof passed but a requirement/named claim remains unmet → outcome is `partial`; run the
  missing exact verifier/contract, or use `--unverified` to explicitly acknowledge incomplete proof

`--unverified` / `--accept-failed` are not shortcuts; they permanently label the outcome so it can
never masquerade as a clean pass later.

### My high-risk change threw an extra warning at verify. Bug?

No — that's `--risk high` working as intended. High-risk changes must clear a stricter bar: a
structural diff review that actually *passed*. If codemap isn't indexed the review
is inconclusive, and Cortex says so rather than waving the change through. Index codemap and
re-verify, or lower the risk band if it was overstated.

### Can I investigate more than once?

Yes. Each round is counted against a small budget (you'll see `rounds 2/3` in `status`). Exceeding
it doesn't hard-block you — it *warns*, so aimless searching becomes visible. The nudge is toward
forming a hypothesis and planning, not toward endless search.

### How do I pause for a human or transfer the task?

Use `cortex decision request` for one bounded choice with at least two options and explicit
consequences. The task enters the resumable `needs_human_decision` phase; `decision answer` records
the choice and returns to the exact paused phase. Use `cortex note` for provenance-bearing context
that should not pause work, and `cortex handoff` for a bounded packet containing the plan,
hypotheses, current assessment, recent evidence, decisions, and structured next actions.

### Is there a UI?

Yes — a read-only terminal UI over the case store:

```bash
cortex studio     # aliases: cortex board, cortex tui
```

It lists sessions from the central store across repositories and shows the selected case's loop,
canonical verification assessment and gaps, pending decision, first structured action,
hypotheses, and bounded recent receipts/evidence. Navigate with arrow/`j`-`k` keys, toggle
active-only with `a`, and quit with `q`. See [Studio](/studio).

## State, privacy, and secrets

### Where does Cortex store state?

In a **central, XDG-organized** location by default, on disk, so every session across every repo is
auditable in one place: `$XDG_STATE_HOME/cortex/sessions/<repo>/<taskId>/` (case, evidence,
hypotheses, plan, receipts, command audit trail, phase history, raw tool output, and a generated
`summary.md`). Config and cache follow XDG too (`$XDG_CONFIG_HOME/cortex`, `$XDG_CACHE_HOME/cortex`);
`$CORTEX_HOME` or a legacy `~/.cortex` collapses them into one dir. Set `cases_dir` /
`CORTEX_CASES_DIR` to keep a project's cases repo-local instead — and then Cortex gitignores them
via `.cortex/.gitignore`. See [The case file](/case-file) and [Configuration](/configuration).

### Does Cortex send my code anywhere?

Cortex does not upload your repository by default: the kernel orchestrates local tools and writes
local case files. One optional exception is cross-case recall. When recall is enabled and Veclite
is available, the adapter sends redacted goal, hypothesis, and resolution text to the configured
`recall.embed_url` to obtain embeddings. Recall queries and indexed verification statements also
use that endpoint. The default endpoint is local Ollama at
`http://localhost:11434`; if you configure a remote endpoint, that text leaves your machine.
Set `recall.enabled: false` to disable this call, or keep `embed_url` on a trusted local endpoint.

Your LLM harness and any other configured specialist tools may have their own network behavior;
review those separately.

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
root → env. See [Configuration](/configuration). Check the resolved paths, budget, and source
files with:

```bash
cortex config
```

### Can Cortex run my repository's tests or lint command?

Yes, but only when an exact argv array is configured in `cortex.yaml` under `verifiers:` **and** the
trusted launcher sets `CORTEX_APPROVE_COMMANDS=1`. Repository config cannot approve itself. Callers
name the configured check (`command:unit`); they cannot supply shell text or append arguments.
Without approval Cortex records `blocked`. Allowed kinds are `unit_test`, `build`, and `lint`, all
on the code surface. See
[Configuration](/configuration#safe-command-verifiers).

### Which agent harnesses does Cortex work with?

Any harness that supports stdio MCP can use Cortex; the kernel and result contract are independent
of the model provider. Cortex tests its MCP schemas in memory and through a real stdio subprocess,
but individual client configuration still varies. Register the server once with mcphub to expose
the same profile to every harness behind that gateway:

```bash
mcphub add cortex cortex serve
mcphub sync --write
```

In gateway mode the tools are namespaced `cortex__<tool>`. Details and recommended tool pins are in
[MCP](/mcp). Run `cortex doctor --probe` to verify the live registration and handshake instead of
assuming a particular client is configured correctly.

### Can I investigate a bug from a screen recording?

Yes, if you have [vidtrace](/adapters) installed. Pass a bundle path or a `vidtrace://` stash id:

```bash
cortex investigate $TID "the checkout button does nothing" --video vidtrace://vt_abc123
```

Cortex runs vidtrace to turn the recording into timestamped evidence and links the visible failure
to the code that owns it, then continues the normal discovery → candidates → structure route.

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

Tasks are append-oriented by design because the history is the audit trail. Choose the operation
that matches your intent:

- `cortex abort <taskId> "<reason>"` stops in-flight work without deleting evidence.
- `cortex archive <taskId>` moves a terminal session out of active views and is reversible with
  `cortex unarchive <taskId>`.
- `cortex rm <taskId>` is a dry run that shows what would be deleted; add `--force` only when you
  intend to permanently remove that terminal session.

Do not assume state lives in the workspace's `.cortex/` directory: the default is the central XDG
store. Run `cortex config` to see the exact session and archive paths.

### Still stuck?

- [Concepts](/concepts) — the model behind the gates.
- [CLI reference](/cli) — every command and flag.
- [GitHub issues](https://github.com/abdul-hamid-achik/cortex) — bugs and questions.
