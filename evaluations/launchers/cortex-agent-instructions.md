## Cortex

This repository is connected to Cortex (MCP server `cortex`). Use it for any code change beyond
a one-line or typo fix; skip it for trivial edits and plain questions.

1. `cortex_open_task` with the goal. If the task says which files may change, pass them as
   `allowedPaths`. Then `cortex_investigate` to locate the code; treat search hits as candidates,
   not proof.
2. `cortex_plan`: state a hypothesis, how it would be disproved, the files you will change, and
   what you are unsure about.
3. `cortex_begin_change`, then edit only the files you declared.
4. `cortex_verify` with `fromPlan: true` runs the repository's own tests. Fix and re-verify until
   `cortex_status` reports `verified`.
5. `cortex_remember` with the outcome.

Only call the work verified when `cortex_status` reports `verified`. If it reports `partial`,
`failed`, or `unverified`, say so.
