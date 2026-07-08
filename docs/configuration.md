# Configuration

Cortex runs with sensible defaults and needs no config file. When you want to tune it, drop a
`cortex.yaml` in your workspace or set `CORTEX_*` environment variables.

## Precedence

Lowest to highest — later wins:

1. Built-in defaults
2. Global config — `$CORTEX_HOME/config.yaml` (or `~/.cortex/config.yaml`)
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
  max_auto_retries_per_tool: 1       # read-only retry budget

# Extra exact strings the redactor always masks (e.g. known secret NAMES).
# Never put secret VALUES here.
redact_literals:
  - MY_INTERNAL_TOKEN_NAME

# Override where case files are written (relative paths resolve against the workspace).
cases_dir: .agent/cases
```

## Environment variables

Env vars have the highest precedence, handy for CI or a one-off run:

| Variable | Overrides |
|---|---|
| `CORTEX_MAX_PARALLEL_CALLS` | `budget.max_parallel_calls` |
| `CORTEX_MAX_INVESTIGATION_ROUNDS` | `budget.max_investigation_rounds` |
| `CORTEX_MAX_RAW_OUTPUT_BYTES` | `budget.max_raw_output_bytes_per_tool` |
| `CORTEX_MAX_EVIDENCE_ITEMS` | `budget.max_evidence_items_returned` |
| `CORTEX_MAX_CANDIDATE_FILES` | `budget.max_candidate_files_returned` |
| `CORTEX_REDACT_LITERALS` | comma-separated redact literals |
| `CORTEX_CASES_DIR` | the case-file directory |
| `CORTEX_HOME` | global state directory (default `~/.cortex`) |

A malformed config file is ignored (Cortex falls back to defaults) rather than failing to start.
