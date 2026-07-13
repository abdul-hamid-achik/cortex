# Configuration

Cortex runs with sensible defaults and needs no config file. When you want to tune it, drop a
`cortex.yaml` in your workspace or set `CORTEX_*` environment variables.

## Precedence

Lowest to highest — later wins:

1. Built-in defaults
2. Global config — `$XDG_CONFIG_HOME/cortex/config.yaml` (or `$CORTEX_HOME` / a legacy `~/.cortex`)
3. Project `.config/cortex.yaml`
4. Project `cortex.yml` / `cortex.yaml`
5. `CORTEX_*` environment variables

Run `cortex config` to see the resolved workspace/storage paths, budget, recall policy, safe
verifier metadata, redaction count, and exactly which files were applied. Verifier names, kinds,
surfaces, and timeouts are shown; executable `argv` is deliberately omitted:

```bash
cortex config          # styled view
cortex config --json   # machine-readable
```

## `cortex.yaml`

Every field is optional; a partial file only overrides what it names.

```yaml
# Tool-use budget — bounds how hard the kernel works per task.
budget:
  max_parallel_calls: 3              # concurrent adapter fan-out (e.g. health probes)
  max_investigation_rounds: 3        # investigate calls before a budget nudge
  max_raw_output_bytes_per_tool: 32768   # per-tool raw-output cap
  max_evidence_items_returned: 12    # evidence items per investigation
  max_candidate_files_returned: 8    # discovery hits per search
  max_auto_retries_per_tool: 1       # read-only retry budget (0 = never retry; mutations never retry)

# Cross-case disproof recall — the fourth memory layer. Best-effort.
recall:
  enabled: true                     # set false to disable recall entirely
  db_path: ~/.local/share/cortex/cases.veclite   # the veclite index (default: XDG data home)
  embed_model: nomic-embed-text     # ollama embedding model
  embed_url: http://localhost:11434/api/embeddings  # ollama embeddings endpoint

# Trusted, read-only code verifiers. Callers choose a NAME; they cannot supply
# or append executable text through the CLI/MCP request.
verifiers:
  unit:
    argv: ["go", "test", "./..."]
    kind: unit_test                  # unit_test | build | lint
    surface: code                    # v0.1 accepts code only
    timeout: 2m
  lint:
    argv: ["golangci-lint", "run"]
    kind: lint
    surface: code
    timeout: 3m

# Extra exact strings the redactor always masks (e.g. known secret NAMES).
# Never put secret VALUES here.
redact_literals:
  - MY_INTERNAL_TOKEN_NAME

# Where case files live. Default: a central, XDG-organized location
# ($XDG_STATE_HOME/cortex/sessions/<repo>/). Set this only to override — e.g. to
# keep a project's cases repo-local, or to pin them somewhere specific:
#   cases_dir: .cortex/cases            # repo-local (relative → under workspace)
#   cases_dir: ~/somewhere/my-project   # absolute / ~ → anywhere
# cases_dir: .cortex/cases
```

## Safe command verifiers

Configured verifiers fill the gap between structural/behavioral tools and repository-specific
tests, builds, or lint checks. They are deliberately configuration-only:

- `argv` is an exact argument array. Cortex invokes the executable directly and never evaluates a
  shell string or metacharacters.
- A CLI/MCP caller may choose only the configured name, never replace or append arguments.
- Configuring argv does **not** authorize it. Repository commands are arbitrary local code and stay
  blocked unless the trusted process launching Cortex sets `CORTEX_APPROVE_COMMANDS=1` (truthy
  values `true`, `yes`, and `on` also work). A repository cannot grant itself this permission.
- Names contain letters, digits, `-`, or `_`; `kind` is `unit_test`, `build`, or `lint`; v0.1
  requires the `code` surface and a positive timeout.
- Defaults for omitted fields are `unit_test`, `code`, and two minutes.
- Invalid verifier policy fails Cortex startup closed instead of silently weakening verification.
- Invalid safety budgets fail closed too: parallel calls, investigation rounds, raw bytes,
  evidence items, and candidate files must be positive; auto-retries may be zero but not negative.

Every configured verifier is added to the default plan. You may also name one explicitly:

```bash
cortex plan $TID \
  --hypothesis "the fix is local :: run the unit verifier" \
  --file internal/auth/callback.go \
  --verify command:unit \
  --uncertainty "browser coverage is still separate"
```

At verify time an approved Cortex process runs only the stored argv and writes a `command:unit`
verifier receipt. Without launcher approval it records a `blocked` receipt instead of executing.
Bind a typed claim to the exact check with MCP `verifier: "command:unit", contract: "unit"`, or with
the matching CLI `--claim-verifier command:unit --claim-contract unit` flags. Command verifiers can
prove code checks; they cannot satisfy browser, terminal, artifact, or secret claims.

## Where sessions live (XDG)

By default Cortex stores every session in a **central, XDG-organized** location, so all your work
across every repo is visible and auditable in one place — see `cortex sessions`, `cortex overview`,
and the `cortex config` **Storage (XDG)** section.

| Purpose | Default path |
|---|---|
| Sessions (case files) | `$XDG_STATE_HOME/cortex/sessions/<repo>/<taskId>/` |
| Global config | `$XDG_CONFIG_HOME/cortex/config.yaml` |
| Cache | `$XDG_CACHE_HOME/cortex/` |

`$CORTEX_HOME` (or a pre-existing `~/.cortex`) collapses config + state + cache into one directory —
the classic single-dir layout — and each dir can be overridden individually with `CORTEX_CONFIG_DIR`
/ `CORTEX_STATE_DIR` / `CORTEX_CACHE_DIR`.

**Repo-local is opt-in.** Set `cases_dir` (or `CORTEX_CASES_DIR`) to keep a project's evidence next
to its code:

| Setting | Location |
|---|---|
| **Default** | `$XDG_STATE_HOME/cortex/sessions/<repo>/<taskId>/` (outside every repo) |
| `cases_dir` | relative → under the workspace (repo-local); absolute/`~/…` → anywhere |
| `CORTEX_CASES_DIR` | same rules; wins over the file |

A pre-existing `<workspace>/.cortex/cases` is honored automatically, so upgrading never strands
active work. When cases are **repo-local**, Cortex writes `.cortex/.gitignore` (`*`) so its own
state never registers as a workspace change; when they live outside the workspace (the default), no
in-repo ignore file is written.

## Environment variables

Env vars have the highest precedence, handy for CI or a one-off run:

| Variable | Overrides |
|---|---|
| `CORTEX_MAX_PARALLEL_CALLS` | `budget.max_parallel_calls` |
| `CORTEX_MAX_INVESTIGATION_ROUNDS` | `budget.max_investigation_rounds` |
| `CORTEX_MAX_RAW_OUTPUT_BYTES` | `budget.max_raw_output_bytes_per_tool` |
| `CORTEX_RECALL_ENABLED` | `recall.enabled` (truthy: 1/true/yes/on) |
| `CORTEX_RECALL_DB` | `recall.db_path` |
| `CORTEX_RECALL_EMBED_MODEL` | `recall.embed_model` |
| `CORTEX_RECALL_EMBED_URL` | `recall.embed_url` |
| `CORTEX_MAX_EVIDENCE_ITEMS` | `budget.max_evidence_items_returned` |
| `CORTEX_MAX_CANDIDATE_FILES` | `budget.max_candidate_files_returned` |
| `CORTEX_MAX_AUTO_RETRIES` | `budget.max_auto_retries_per_tool` |
| `CORTEX_APPROVE_COMMANDS` | trusted-launcher approval for repository-configured verifier argv; unset denies execution |
| `CORTEX_REDACT_LITERALS` | comma-separated redact literals |
| `CORTEX_CASES_DIR` | the case-file directory (default: the central XDG sessions dir) |
| `CORTEX_HOME` | collapse config + state + cache into one dir (default: the split XDG dirs, or a legacy `~/.cortex`) |
| `CORTEX_CONFIG_DIR` / `CORTEX_STATE_DIR` / `CORTEX_CACHE_DIR` | override one XDG directory individually |

A file that cannot be parsed, contains an unknown field, or declares invalid policy is retained as
a configuration problem. Kernel construction and `cortex config` then fail closed with the source
path and validation error instead of silently falling back to weaker defaults.
