# Empirical trajectory runner

Cortex includes a separate, opt-in harness for running one fixed engineering scenario under
controlled tool-exposure arms. It is evaluation infrastructure, not a Cortex runtime surface: it
does not change kernel behavior, MCP profiles, leases, verification policy, or completion
authority, and it is not included in release archives.

The runner complements `task eval`. The checked-in `task eval` pairs are deterministic fixtures
that calibrate the scoring model. A trajectory run executes a real launcher against isolated
repository copies and evaluates the result with an independent oracle. Neither kind of run, by
itself, establishes general model uplift.

## Controlled arms

A schema-v1 manifest may compare two to four arms. `raw_tools` is always first and is the scoring
baseline:

| Arm | Intended launcher condition |
|---|---|
| `raw_tools` | the pinned model with direct repository and tool access, without Cortex |
| `cortex` | the same model and budgets with Cortex |
| `cortex_bob` | Cortex plus a Bob repository-contract condition supplied by the launcher |
| `cortex_bob_local_agent` | Cortex and Bob plus local-agent structured continuations |

The runner labels and measures these conditions; the trusted launcher implements their actual tool
exposure. In particular, naming a Bob arm does not add a Bob adapter to Cortex or make Bob output
behavioral proof. If a requested dependency is unavailable, the launcher must report the arm as
`blocked` or `incomplete` rather than silently substituting another condition.

All arms receive the same goal, immutable acceptance criteria, repository fixture, pinned model
configuration, and budgets. Each arm starts from a separate copy of the fixture with a deterministic
initial Git commit. Every launcher result also declares the executable toolchain actually exposed
to that arm. Cortex independently resolves and hashes those binaries: `cortex` is mandatory in the
Cortex arms, `bob` in the Bob arms, and `local-agent` in the structured-continuation arm; those
managed tools are rejected from lower arms where they would contaminate the comparison.

## Manifest and authority are separate

The scenario manifest is strict YAML. The sample at
[`evaluations/terminal-command-regression.yaml`](https://github.com/abdul-hamid-achik/cortex/blob/main/evaluations/terminal-command-regression.yaml)
pins:

- a repository fixture and its SHA-256 tree digest;
- the goal, acceptance criteria, behavioral surfaces, and allowed changed paths;
- model identifier and build, temperature, seed (or why the provider cannot seed), and context
  budget;
- the selected arms and tool, wall-time, oracle, trace, and cost ceilings;
- exact command-oracle `argv` arrays and Glyphrun specs, each mapped to acceptance criteria.

The manifest cannot name the arm launcher, set environment variables, or approve its own
execution. Its paths are relative and bounded, unknown schema fields and future versions are
rejected, every acceptance criterion must have oracle coverage, and the fixture digest is checked
before execution. Scenario authors must list every fixture file the oracle trusts in
`oracle.protected_paths`; Cortex validates and freezes that list but cannot infer every transitive
test dependency on the author's behalf.

Oracle commands still execute local code once a run is approved. Treat a trajectory manifest and
its fixture as trusted test-harness input and review their exact `argv` arrays before opting in.
Commands and launchers are invoked directly without a shell. Before each arm's oracle phase, Cortex
resolves every available command or Glyphrun executable to an absolute, symlink-resolved path,
records its digest, rechecks that digest immediately before execution, and executes the resolved
path. A missing executable or changed digest produces an honest `blocked` result for that oracle;
it cannot become green and does not prevent independent available oracles from running.

Launcher authority lives in a separate, operator-controlled YAML file containing only a schema
version and an exact `argv`:

```yaml
schema_version: 1
argv:
  - /absolute/path/to/trusted-arm-launcher
  - --protocol
  - cortex-trajectory-v1
```

The launcher executable must be an absolute, clean path. Before creating an arm workspace, Cortex
resolves symlinks once, records the original redacted argv plus the resolved binary digest, and
executes that resolved path. It rechecks the digest immediately before each arm so repository code
cannot replace or redirect the trusted launcher.

The runner sends one bounded JSON request on stdin for each arm. The launcher returns one strict
schema-v1 JSON value on stdout, including the echoed request digest, run status, reported
completion, the effective model and executable toolchain, trusted-launcher
evidence/disproof/recovery/boundary instrumentation, verifier choices, receipts, tool calls, and any
available token/cost counts. The effective identifier, build, temperature, seed policy, and context
budget must exactly match the manifest. Toolchain paths must be absolute and outside the mutable arm
workspace. Cortex requires arm-specific names, independently verifies each path/digest, and retains
the reported version as trusted-launcher attestation. Binary identity is verified; whether the
trusted launcher actually used every reported executable remains harness instrumentation. Unknown
fields, multiple JSON values, invalid statuses, negative counts, budget overruns, and a mismatched
request digest fail the arm. Model tokens and estimated cost remain absent when the provider cannot
measure them; the runner does not invent values or score missing cost as zero.

The child launcher inherits the operator environment because it may need provider credentials, but
all inherited `CORTEX_*` variables—including approval gates—are removed before the harness adds its
own isolated roots. The harness freezes the operator's Cortex configuration once, records its
digest, and gives every launcher and oracle a distinct private copy whose digest is checked before
and after use. Every arm also receives distinct owner-only Cortex state/case/cache and XDG
state/cache/data roots; oracle processes use a second isolated root and remove inherited variables
both when their names look sensitive and when their values match known secret shapes. A private
configuration mutation invalidates only that arm and cannot contaminate the next one. This prevents
a Cortex arm from resuming another arm's case or consuming another run's mutable cache. A launcher
that needs to authorize a nested Cortex command verifier must do so explicitly within its own
reviewed process policy.
Post-launch workspace snapshots reject symlinks and non-regular files, stream hashes under the
arm deadline, and cap each file at 256 MiB, the complete snapshot at 1 GiB, cardinality at 100,000
entries, and aggregate relative-path storage at 16 MiB. Fixture freeze/copy uses the same streaming
size and cardinality limits. Launcher and oracle processes run in isolated process groups so a
timeout kills the child and descendants that remain in that group; execution fails closed on
platforms where that containment is not available. A hostile same-user child that deliberately
creates a new session remains outside this trusted-harness containment model. Manifest validation
remains read-only on those platforms.

## Validate and run

Validation is read-only and does not require execution approval:

```bash
task trajectory-validate \
  MANIFEST=evaluations/terminal-command-regression.yaml
```

Real execution is deliberately outside ordinary unit CI. A trusted operator must both supply the
separate launcher config and set the process-level approval gate:

```bash
CORTEX_APPROVE_TRAJECTORY=1 task trajectory \
  MANIFEST=evaluations/terminal-command-regression.yaml \
  LAUNCHER=/secure/operator/launcher.yaml
```

`CORTEX_APPROVE_TRAJECTORY` grants execution authority to the reviewed scenario for that process;
repository YAML cannot set it. A real launcher may contact a model provider, so network access,
credentials, provider terms, and spend remain the operator's responsibility. Unit tests use fake
processes and do not perform network runs.

The underlying command also accepts `--state-root` and `--run-id` when an operator needs an
isolated output location or stable run label:

```bash
CORTEX_APPROVE_TRAJECTORY=1 go run ./cmd/cortex-trajectory run \
  --manifest evaluations/terminal-command-regression.yaml \
  --launcher /secure/operator/launcher.yaml \
  --state-root /secure/evaluation-state \
  --run-id provider-build-2026-07-15
```

## Oracle-driven reports

The launcher cannot certify its own success. After each arm stops, the harness measures its diff,
rebuilds a separate oracle workspace from the frozen fixture baseline, and overlays only changed
files that match `allowed_changes`. Out-of-bound changes are never copied into the oracle workspace;
they mark scope drift and invalidate a green oracle result. Exact `oracle.protected_paths`—such as
tests, modules, or fixture-owned executables—also remain frozen and any attempted change invalidates
oracle integrity. The retained Glyphrun specs are the copies captured before arm execution. This
keeps model-modified tests, oracle-generated files, and scope drift from certifying the arm or
changing the measured model diff. The harness then derives:

- oracle success and reported-versus-expected completion honesty;
- changed files, wrong files, and scope drift against `allowed_changes`;
- verifier selection plus false or repository-stale passing receipts;
- whether the trusted launcher actually instrumented a declared change boundary;
- tool calls, launcher latency, separately reported oracle/total harness latency, measured
  input/output tokens, estimated cost, and human interventions;
- the existing evidence, disproof, recovery, scope, verification, completion, and cost scorecard.

`failed`, `blocked`, `timeout`, and `incomplete` arms remain in the report. Oracle failures,
`not_run`, blocked, inconclusive, and stale evidence never become passes. Scores compare every
candidate with `raw_tools`, while each arm's independent oracle result remains visible rather than
being folded into a self-reported success claim.

The nested paired score keeps the calibration package's historical JSON field names
`cortex` and `cortexQuality`. In a trajectory report those fields mean the selected candidate arm;
the enclosing `candidateArm` field is authoritative.

By default, owner-only results are stored at:

```text
$XDG_STATE_HOME/cortex/trajectories/<scenario-id>/<run-id>/
  report.json
  manifest.json
  launcher.json
  fixture-baseline/
  workspaces/<arm>/
  oracle-workspaces/<arm>/
  oracle-specs/...
  runtime/cortex-config-baseline/config.yaml
  runtime/{launcher|oracle}/<arm>/cortex-config/config.yaml
  runtime/{launcher|oracle}/<arm>/{cortex-state,cortex-cache,cortex-cases,xdg-*}/
  traces/<arm>.stderr.txt
```

Launcher stderr is bounded and redacted before it is retained. If capture exceeds the manifest's
trace ceiling, the trace is omitted atomically and marked truncated instead of saving a misleading
prefix. Launcher protocol stdout is bounded and decoded, not published as a raw trace. Keep real
traces and reports out of public documentation and inspect them for scenario-specific sensitive
data before sharing.

Every report binds the semantic manifest, frozen repository fixture, copied oracle specs, frozen
and per-use Cortex configuration digests, redacted launcher configuration, the resolved launcher
digest and every available oracle binary digest, per-arm effective toolchain names/versions/digests,
effective model, harness version/VCS state, Go version, platform, and each arm request by SHA-256.
Unavailable oracle identity remains explicit and its result remains blocked. The retained
oracle-spec snapshot is what the run executes. Boundary and recovery scores are trusted launcher
instrumentation and are applicable only when that launcher actually performed the documented
declaration or interruption/resume probe; model prose alone must never set them.

## Interpreting results

A valid report is an observation that must remain paired with the exact reviewed manifest and
launcher config used to produce it. It is not automatically a product claim. Any statement about
model uplift should use repeated, reviewed runs across representative scenarios, report missing and
failed arms, account for variance and human intervention, and preserve the configuration and raw
evidence needed to reproduce the conclusion.

## MCP profile decision gate

CTX-5 is not implemented. This release contains neither a reviewed empirical result set comparing
the profile alternatives nor an explicit selection of a `lite` design. Cortex therefore continues
to expose only the contract-tested `agent` and `all` profiles. The fact that `agent` exposes 17
tools is not evidence that a smaller surface would help.

Deterministic fixtures do not open this gate. Public conformance goldens, exact profile-set tests,
`task eval`, and fake-runner trajectory tests validate schemas, wiring, scoring, and failure
handling. They do not measure how a real model uses a tool surface and cannot support a profile or
uplift decision.

The required empirical experiment must compare all four conditions:

1. the current `agent` profile directly;
2. the same current profile through MCPHub with exactly the six recommended pins
   (`cortex__cortex_open_task`, `cortex__cortex_investigate`, `cortex__cortex_plan`,
   `cortex__cortex_begin_change`, `cortex__cortex_verify`, and `cortex__cortex_status`);
3. one frozen candidate `lite` profile with its exact tool set declared before the run; and
4. lazy discovery of non-core operations driven by structured `actions`.

Use the same reviewed scenarios, repository snapshots, model identifier/build, temperature and
seed policy, context and tool budgets, timeouts, tool schemas, independent oracles, and treatment
of failed/incomplete arms. Report context consumption, oracle success, completion honesty,
recovery after interruption, and human-intervention behavior rather than relying on model
self-report.

Implement a profile only if reviewed results show a meaningful quality improvement or meaningful
context savings **without materially worse recovery or human collaboration**, and the user then
explicitly selects the profile design. If approved later, profile wiring may change exposure only;
it must reuse the same tool schemas and handlers and must not change kernel semantics. Until both
the evidence and design-selection conditions are met, no `--profile lite` or profile-related uplift
claim is justified.
