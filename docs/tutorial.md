# Tutorial: your first task, end to end

This is a hands-on walkthrough. In about ten minutes you'll take a real (tiny) bug from
"something's wrong" to a completed, remembered task — and, more importantly, you'll *see why*
Cortex makes you work the way it does. Every command below is real; the output is copied from an
actual run.

If you just want the command list, the [Quick Start](/quick-start) is the condensed version. This
page is the one to read if the loop doesn't yet feel natural.

::: tip What you'll learn
- The six actions — `start`, `investigate`, `plan`, `verify`, `remember`, `status` — and the order they must happen in.
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
working. This tutorial runs fine with *zero* specialist tools installed; you'll just see warnings
where a richer tool would have added evidence. That's the point: Cortex never fabricates a missing
tool's output.

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

## 1. Start — open a case and orient

```bash
cortex start "Fix post-login checkout redirect" --surface code --surface browser --risk high
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

- Cortex opened a **case** and printed its `taskId`. Everything from here on hangs off that id. Copy it — you'll pass it to every later command. (`cortex list` shows it again if you lose it.)
- It recorded git identity (repo, branch, and the **baseline commit** `e532869`) as the first piece of evidence, `high` confidence. That baseline is what "scope drift" is measured against later.
- It advanced the phase machine to `investigating`. You can't skip ahead — try to `plan` right now and it'll refuse, because you haven't investigated.
- `--surface code --surface browser` declares where the fix is user-visible. `--risk high` matters: it makes the verification bar stricter, as you'll see in step 4.

::: info Save the id in a shell variable
Every command below takes the task id. To follow along:
```bash
TID=task_06FKK4RXWKRVFWV0   # use the id your run printed
```
:::

## 2. Investigate — discover by meaning, then resolve structure

```bash
cortex investigate $TID "where is the OAuth return URL handled after login"
```

```ansi
✓ [investigating] investigated "where is the OAuth return URL handled after login" via cairntrace→codemap: 0 evidence items recorded (prove observed browser failure, then map UI evidence to code)
  task task_06FKK4RXWKRVFWV0

Warnings
  ⚠ vecgrep: Error: not in a vecgrep project: run 'vecgrep init' first

Next
  → read raw evidence with cortex read-evidence <taskId> <evidenceId> when you need detail
  → cortex plan — state a hypothesis with a disproof path, a change boundary, and a verification plan
```

You ask questions in **plain language**; Cortex routes them. A vague behavioral question goes to
discovery tools first (semantic search) and then to structural tools (call graphs, impact). Ask
about a specific symbol and it resolves structure directly. You never pick the tool — you describe
what you want to know.

Here `vecgrep` isn't indexed, so its adapter warns instead of inventing hits — nothing is
fabricated. With the specialist tools installed and indexed you'd get a handful of candidate
locations recorded as evidence.

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
  --hypothesis "returnTo is dropped in HandleCallback before the redirect :: run the login-from-checkout browser flow and confirm the final URL is not the return path" \
  --file src/auth/callback.go --symbol HandleCallback \
  --confidence medium \
  --boundary-reason "the redirect is computed here" \
  --uncertainty "unsure whether signed-state encoding also strips ReturnTo"
```

```ansi
✓ [planned] plan accepted: 1 hypothesis, boundary of 1 file(s) / 1 symbol(s), 2 verifiers required
  task task_06FKK4RXWKRVFWV0

Hypotheses
  hyp_06FKK4TKNVY0WZYQ [med] returnTo is dropped in HandleCallback before the redirect

Next
  → make your edits within the declared boundary — expand the plan if scope changes
  → cortex verify — run the required verifiers and check for scope drift
```

The plan gate enforces two invariants:

1. **Every hypothesis needs a disproof path.** The `statement :: disproof` shorthand pairs a claim with *how it could be proven wrong* (you can also use paired `--hypothesis` / `--disprove` flags). A hypothesis you can't imagine falsifying isn't a hypothesis — it's a belief, and the gate rejects it.
2. **A change task needs a boundary.** `--file` / `--symbol` declare the files and symbols you expect to touch. Editing *outside* this set later is flagged as **scope drift**. This is how "small fix" stays small.

`--uncertainty` is required: you must state, out loud, what you still don't know. Here we admit we
haven't ruled out the state encoder. The plan is now recorded and the task is `planned`.

## 4. Edit, then verify

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

Then ask Cortex to check your work:

```bash
cortex verify $TID --claim "the OAuth callback preserves the return URL after login"
```

```ansi
✓ [verifying] 0/1 claims verified; scope within_boundary
  task task_06FKK4RXWKRVFWV0

Evidence
  ev_06FKK4W2REZZF5GW [med] diff touches 1 file (codemap not indexed — no blast radius; run `codemap index`)

Warnings
  ⚠ codemap: project not indexed — run 'codemap index' for blast radius and test selection
  ⚠ high-risk change requires a structural diff review that passed, but codemap review is inconclusive — run `codemap index` and re-verify (SPEC §13.3)
  ⚠ claim "the OAuth callback preserves the return URL after …" needs a browser verifier that was not run

Next
  → provide the missing verifier spec (browser_spec / terminal_spec) and re-run cortex verify
  → cortex remember — persist the outcome, evidence, and uncertainty once verification is adequate
```

Read those warnings closely — this is Cortex being *useful by refusing to lie*:

- **`scope within_boundary`** — the diff only touched `src/auth/callback.go`, exactly what you declared. Had you edited another file, it would say `drifted` and name it.
- **`0/1 claims verified`** — you *claimed* the callback preserves the return URL, but the browser verifier that could prove it didn't run (no cairntrace spec, no browser installed). Cortex records that claim as `not_run` — **never** as `passed`. A claim without a verifier is an open question, not a success.
- **The `--risk high` you set in step 1 pays off here:** the [§13.3] escalation demands a structural diff review that actually passed. Because codemap isn't indexed, the review is *inconclusive*, and Cortex says so rather than waving the high-risk change through.

To make this claim genuinely pass, you'd give verify a spec:

```bash
cortex verify $TID \
  --claim "the OAuth callback preserves the return URL after login" \
  --browser-spec specs/checkout_return.yml   # a cairntrace flow that logs in from checkout
```

…and index codemap so the diff review is conclusive. Then the same command reports the claim
`passed` with a receipt pointing at the flow that proved it.

## 5. Remember — complete the task

Completion is itself gated: **a task cannot complete without a verification receipt.** In this
tutorial we deliberately didn't run the browser verifier, so we have to *acknowledge that
explicitly* with `--unverified` — otherwise Cortex refuses to finish:

```bash
cortex remember $TID \
  "ReturnTo was dropped in HandleCallback; now preserved when present. Confirmed by diff review." \
  --tag auth --tag oauth --importance 0.7 --unverified
```

```ansi
✓ [complete] task task_06FKK4RXWKRVFWV0 complete: ReturnTo was dropped in HandleCallback; now preserved when present. Confirmed by diff review.
  task task_06FKK4RXWKRVFWV0

Next
  → summary written to …/.cortex/cases/task_06FKK4RXWKRVFWV0/summary.md
```

`--unverified` is not a loophole — it's the honest label. It records, permanently, that this
outcome was *not* fully verified. In real high-risk work you'd instead go back, provide the spec,
and let verify write a real receipt. Cortex never lets an unverified outcome quietly masquerade as
a verified one; the worst it allows is an outcome that is *clearly marked* unverified.

On completion Cortex writes a human-readable `summary.md` and stores a durable memory (tagged
`auth`, `oauth`) so a future task can recall it.

## Look at what you built

Everything the task did is on disk, append-only, under `.cortex/cases/<taskId>/`:

```
case.json          the task: goal, mode, risk, phase, workspace, surfaces
evidence.jsonl     every evidence record, in order, with provenance + confidence
hypotheses.json    hypotheses and their disproof paths
plan.json          the change boundary and required verifiers
verification.json  the receipts: what was checked, and the not_run/passed status of each claim
commands.jsonl     the audit trail: every tool call, with its action class
raw/               the raw tool output each evidence record points back to
summary.md         the generated human summary (below)
```

Inspect a live task at any phase:

```bash
cortex status $TID --detail full
```

```ansi
✓ [complete] task task_06FKK4RXWKRVFWV0 is complete (Fix post-login checkout redirect)

Task
  mode change · risk high · repo demo
  branch main @ e532869
  evidence 2
  rounds   1/3

Unresolved hypotheses
  [med] returnTo is dropped in HandleCallback before the redirect

Missing verification
  ✗ codemap_review
  ✗ cairntrace_flow
```

Notice status is candid even after completion: the hypothesis is still `[med]` (we never *proved*
it, only made a plausible fix), and it lists the verifiers that never ran. Nothing is rounded up.

And the generated summary:

```markdown
# Fix post-login checkout redirect

- **Task:** `task_06FKK4RXWKRVFWV0`
- **Repository:** demo @ e532869 (main)
- **Mode:** change · **Risk:** high · **Status:** complete

## Outcome
ReturnTo was dropped in HandleCallback; now preserved when present. Confirmed by diff review.

## Verification
- [inconclusive] **structural review of the diff** — codemap (code)
- [not_run] **the OAuth callback preserves the return URL after login** — cairntrace (browser)
```

## What to take away

You just ran the loop Cortex enforces on every task:

**orient → investigate → plan → change → verify → remember.**

It isn't a suggestion in a prompt — each arrow is a real gate. You can't plan without
investigating, can't declare success without a receipt, and can't drift outside your boundary
without it showing. The result is a task that is **auditable by construction**: anyone can open
`.cortex/cases/<taskId>/` and reconstruct exactly what was believed, what was checked, and what is
still uncertain.

## Where to go next

- **[Concepts](/concepts)** — the phase machine, evidence model, and invariants in depth.
- **[FAQ](/faq)** — quick answers to the questions this tutorial probably raised.
- **[CLI reference](/cli)** — every command and flag.
- **[MCP server](/mcp)** — drive this exact loop from an agent harness instead of the shell.
- **[Configuration](/configuration)** — budgets, redaction literals, and case-file location.
