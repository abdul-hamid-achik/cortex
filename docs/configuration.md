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

Run `cortex config` to see the resolved values and exactly which files were applied:

```bash
cortex config          # styled view
cortex config --json   # machine-readable
```

## `cortex.yaml`

Every field is optional; a partial file only overrides what it names.

```yaml
# Tool-use budget (SPEC §7.3) — bounds how hard the kernel works per task.
budget:
  max_parallel_calls: 3              # concurrent adapter fan-out (e.g. health probes)
  max_investigation_rounds: 3        # investigate calls before a budget nudge
  max_raw_output_bytes_per_tool: 32768   # per-tool raw-output cap
  max_evidence_items_returned: 12    # evidence items per investigation
  max_candidate_files_returned: 8    # discovery hits per search
  max_auto_retries_per_tool: 1       # read-only retry budget (0 = never retry; mutations never retry)

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
| `CORTEX_MAX_EVIDENCE_ITEMS` | `budget.max_evidence_items_returned` |
| `CORTEX_MAX_CANDIDATE_FILES` | `budget.max_candidate_files_returned` |
| `CORTEX_MAX_AUTO_RETRIES` | `budget.max_auto_retries_per_tool` |
| `CORTEX_REDACT_LITERALS` | comma-separated redact literals |
| `CORTEX_CASES_DIR` | the case-file directory (default: the central XDG sessions dir) |
| `CORTEX_HOME` | collapse config + state + cache into one dir (default: the split XDG dirs, or a legacy `~/.cortex`) |
| `CORTEX_CONFIG_DIR` / `CORTEX_STATE_DIR` / `CORTEX_CACHE_DIR` | override one XDG directory individually |

A malformed config file is ignored (Cortex falls back to defaults) rather than failing to start.
