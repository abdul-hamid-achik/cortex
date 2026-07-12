# Tutorial: your first task, end to end

This is a hands-on walkthrough. In about ten minutes you'll take a real (tiny) bug from
"something's wrong" to a completed, remembered task — and, more importantly, you'll *see why*
Cortex makes you work the way it does. Every command below is real. IDs, tool findings, and exact
output vary with your repository and the specialist tools installed.

If you just want the command list, the [Quick Start](/quick-start) is the condensed version. This
page is the one to read if the loop doesn't yet feel natural.

::: tip What you'll learn
- The golden path — `open`, `investigate`, `plan`, `begin-change`, `verify`, `remember` — and how retry safety and bounded ownership fit into it.
- Why Cortex records search results as *candidates* and refuses to call anything *passed* until a verifier ran.
- How the case file accumulates an auditable trail you can read at any point.
:::

## Setup

Install the binary (a single pure-Go executable):

```bash
go install github.com/abdul-hamid-achik/cortex/cmd/cortex@latest
# or, from a clone: task build  →  ./bin/cortex
```

Cortex composes specialist tools (codemap, vecgrep, cairntrace, glyphrun, fcheap, tvault, vidtrace)
but **none are required**. Check what you have:

```bash
cortex doctor
```

Anything missing simply degrades — the adapter reports `tool_unavailable` and the loop keeps
working. You can exercise every gate with *zero* specialist tools installed, but a normal verified
completion requires current proof that satisfies every planned requirement and named claim. The primary path below uses an
indexed **codemap** structural review. If codemap is unavailable, follow the separately labeled
[degraded path](#degraded-path-when-no-verifier-can-run) instead; Cortex never fabricates a tool's
output.

### A repo to practice on

Let's plant a bug we can actually fix. Any git repo works; here's a self-contained one:

```bash
mkdir demo && cd demo && git init -q
mkdir -p src/auth
cat > src/auth/callback.go <<'EOF'
package auth

// HandleCallback finalizes the OAuth login and redirects the user onward.
func HandleCallback(state SignedState) string {
	// returnTo is dropped here — always sends the user to "/".
	return "/"
}

type SignedState struct{ ReturnTo string }
EOF
git add -A && git commit -qm "init demo app"
```

The bug: after logging in from the checkout page, the user is always bounced to `/` instead of
back to checkout, because `HandleCallback` ignores `state.ReturnTo`.

For the verified path, initialize codemap in this demo repository before editing:

```bash
codemap index
```

If `cortex doctor` reports codemap unavailable, continue through the workflow to see the gates, then
use the degraded branch in step 5 rather than treating an unavailable review as a pass.

## 1. Open — resume safely or start once

```bash
cortex open "Fix post-login checkout redirect" \
  --surface code --risk low \
  --actor tutorial-agent --idempotency-key tutorial-oauth-redirect
```

```ansi
✓ [investigating] started task task_06FKK4RXWKRVFWV0 (Fix post-login checkout redirect); oriented and ready to investigate
  task task_06FKK4RXWKRVFWV0

Evidence
  ev_06FKK4RY0N5DFRNF [high] workspace demo on branch main at e532869

Next
  → cortex investigate — discover by meaning, then resolve structure
  → treat search output as candidates, not proof
```

A few things just happened:

- Cortex opened a **case** and printed its `taskId`. Retry the same command after a lost response and
  the idempotency key returns this case instead of creating a duplicate. Without a key, `open`
  resumes the newest active case matching goal, mode, workspace, and branch. Use `start` only when
  you deliberately need a fresh case.
- Everything from here on hangs off the task ID. Copy it — you'll pass it to every later command.
  (`cortex list` shows it again if you lose it.)
- It recorded git identity (repo, branch, and the **baseline commit** `e532869`) as the first piece of evidence, `high` confidence. That baseline is what "scope drift" is measured against later.
- It advanced the phase machine to `investigating`. An explicit `investigate` call is the normal way
  to gather evidence, and you can repeat it as needed. It is not a separate mandatory counter: if
  you already have enough evidence, the kernel permits a valid plan while the case is in this phase.
- `--surface code` declares the proof surface used by this small demo. A real checkout application
  should also declare `--surface browser` and verify the redirect with a real Cairntrace flow.

::: info Save the id in a shell variable
Every command below takes the task id. To follow along:
```bash
TID=task_06FKK4RXWKRVFWV0   # use the id your run printed
ACTOR=tutorial-agent
```
:::

## 2. Investigate — discover by meaning, then resolve structure

```bash
cortex investigate $TID "where is the OAuth return URL handled after login"
```

You ask questions in **plain language**; Cortex routes them. A vague behavioral question goes to
discovery tools first (semantic search) and then to structural tools (call graphs, impact). Ask
about a specific symbol and it resolves structure directly. You never pick the tool — you describe
what you want to know.

The evidence and warnings depend on local indexes. If `vecgrep` is unavailable or this repository is
not indexed, its adapter warns instead of inventing hits. With the discovery tools installed and
indexed, Cortex records the returned locations as candidates and feeds the strongest ones into
structural expansion.

> **Search results are candidates, never proof.** Every hit is stored with a confidence band
> (`low`/`medium`), and confirming a hypothesis takes more than "search pointed here." That
> discipline is the whole reason Cortex exists — it's what stops an agent from "finding" a cause
> and confidently editing the wrong file.

You can investigate as many times as you need; each round is counted against a small budget
(you'll see `rounds 1/3` in status) so aimless searching becomes visible rather than free.

## 3. Plan — the gate

This is the step people are tempted to skip, so Cortex makes it a hard gate.

```bash
cortex plan $TID \
  --hypothesis "returnTo is dropped in HandleCallback before the redirect :: run an indexed structural diff review and inspect the affected symbols" \
  --file src/auth/callback.go --symbol HandleCallback \
  --confidence medium \
  --boundary-reason "the redirect is computed here" \
  --uncertainty "unsure whether signed-state encoding also strips ReturnTo"
```

```ansi
✓ [planned] plan accepted: 1 hypothesis, boundary of 1 file(s) / 1 symbol(s), 1 verifier required
  task task_06FKK4RXWKRVFWV0

Hypotheses
  hyp_06FKK4TKNVY0WZYQ [med] returnTo is dropped in HandleCallback before the redirect

Next
  → cortex begin-change — claim bounded change ownership before editing
  → make your edits within the declared boundary — expand the plan if scope changes
  → cortex verify — run the required verifiers and check for scope drift
```

The plan gate enforces two invariants:

1. **Every hypothesis needs a disproof path.** The `statement :: disproof` shorthand pairs a claim with *how it could be proven wrong* (you can also use paired `--hypothesis` / `--disprove` flags). A hypothesis you can't imagine falsifying isn't a hypothesis — it's a belief, and the gate rejects it.
2. **A change task needs a boundary.** `--file` / `--symbol` declare the files and symbols you expect to touch. Editing *outside* this set later is flagged as **scope drift**. This is how "small fix" stays small.

`--uncertainty` is required: you must state, out loud, what you still don't know. Here we admit we
haven't ruled out the state encoder. The plan is now recorded and the task is `planned`.

## 4. Begin change — claim bounded ownership

Before editing, identify the writer and acquire the case's expiring lease:

```bash
cortex begin-change $TID --actor $ACTOR
```

The default lease lasts 15 minutes. Retrying `begin-change` as the same actor is safe; supplying a
TTL also renews the same owner's heartbeat. A different actor is rejected until the lease is
released or expires, so two agents cannot both believe they own the change. For a longer manual
edit, use `cortex lease renew $TID --actor $ACTOR --ttl 30m`.

The case snapshot also has an optimistic revision. Lease acquisition and other coordination writes
reload after a revision conflict instead of overwriting a newer process's state.

## 5. Edit, then verify an exact contract

Now make the fix — *within the boundary you declared*:

```go
// src/auth/callback.go
func HandleCallback(state SignedState) string {
	if state.ReturnTo != "" {
		return state.ReturnTo
	}
	return "/"
}
```

Then ask Cortex to check your work. A typed claim names the surface, verifier, and exact contract;
the actor must match the active lease:

```bash
cortex verify $TID \
  --claim "HandleCallback returns state.ReturnTo when it is present" \
  --claim-surface code \
  --claim-verifier codemap \
  --claim-contract codemap_review \
  --actor $ACTOR
```

For the primary path, an indexed codemap review should produce a passing code-surface receipt and
report `scope within_boundary`. Exact facts depend on the index and diff. Check the persisted view
instead of relying on a copied transcript:

```bash
cortex show $TID
cortex status $TID
```

Continue with normal completion only when the code receipt is `passed` and `status` does not list
`codemap_review` under **Missing verification**. If the review is inconclusive because codemap is
unindexed, run `codemap index` and repeat `verify`. If the review fails, fix the change and repeat it;
a failed receipt is evidence that the claim did not hold.

If a planned change intentionally has no diff, `verify` refuses it unless you add `--no-op`.
That flag acknowledges the intentional no-op; it does not manufacture a passing receipt or a
`verified` assessment.

## 6. Remember — complete the task

Completion is itself gated: a normal completion requires the canonical assessment to be
`verified`—current proof exists and every required verifier and named claim is satisfied.
After the indexed structural review passes, persist the outcome without an override:

```bash
cortex remember $TID \
  "ReturnTo was dropped in HandleCallback; now preserved when present and verified by structural review." \
  --tag auth --tag oauth --importance 0.7
```

This is the golden path: Cortex writes `summary.md`, stores durable memory, releases the active
change lease, and labels the outcome `verified` because every required verifier and named claim is
satisfied by current proof.

### Degraded path when no verifier can run

If codemap is unavailable, the earlier `verify` command records `not_run`, `blocked`, or
`inconclusive` rather than inventing a pass. `remember` then refuses normal completion. Only when no
relevant verifier can genuinely run should you acknowledge that limitation explicitly:

```bash
cortex remember $TID \
  "ReturnTo was dropped in HandleCallback; now preserved, but no verifier was available." \
  --tag auth --tag oauth --importance 0.7 --unverified
```

`--unverified` is not a substitute for a failed check. It permanently labels the outcome as
unverified. If a verifier ran and failed, fix the change and re-run it; use `--accept-failed` only
when you intentionally want to preserve a completed **failed** outcome. If some proof passed but a
requirement or named claim remains unmet, the canonical assessment is `partial` and normal
completion still refuses it.

## Look at what you built

Everything the task did is on disk in the central case store. Run `cortex config` to print the exact
path; the default layout is:

```
$XDG_STATE_HOME/cortex/sessions/<repo>/<taskId>/
  case.json          task state, optimistic revision, actor/linkage, lease, boundary
  decisions.json     bounded human questions, options, consequences, and answers
  evidence.jsonl     every evidence record, in order, with provenance + confidence
  hypotheses.json    hypotheses and their disproof paths
  plan.json          the change boundary and required verifiers
  verification.json  receipts: what was checked and the status of each claim
  commands.jsonl     the audit trail: every tool call, with its action class
  phases.jsonl       phase-transition history
  raw/               redacted raw tool output referenced by evidence records
  summary.md         the generated human summary
```

Inspect a live task at any phase:

```bash
cortex status $TID --detail full
cortex show $TID
```

The verified path shows a passing code receipt. The degraded path remains candid: it shows the
missing verifier and the explicit unverified completion instead of rounding either state up to a
pass. The generated `summary.md` records the actual receipts from your run.

## What to take away

You just ran the loop Cortex enforces on every task:

**open → investigate → plan → begin change → verify → remember.**

It isn't a suggestion in a prompt — planning, changing, verification, and completion are real gates.
`open` either resumes the matching case or places a new one in the investigating phase; call
`investigate` whenever you need more evidence, or plan directly when you already have enough
evidence to state a falsifiable hypothesis.
You still cannot declare a normal completion until the canonical assessment is `verified`, and
scope drift remains visible. The result is **auditable by construction** through `cortex show`,
`cortex timeline`, Studio, or the case files in the central store.

## Where to go next

- **[Concepts](/concepts)** — the phase machine, evidence model, and invariants in depth.
- **[FAQ](/faq)** — quick answers to the questions this tutorial probably raised.
- **[CLI reference](/cli)** — every command and flag.
- **[MCP server](/mcp)** — drive this exact loop from an agent harness instead of the shell.
- **[Studio](/studio)** — supervise sessions across repositories from the human operator surface.
- **[Configuration](/configuration)** — budgets, redaction literals, and case-file location.
