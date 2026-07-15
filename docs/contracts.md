# Public conformance contracts

Cortex publishes a versioned JSON fixture corpus under
[`contracts/v1`](https://github.com/abdul-hamid-achik/cortex/tree/main/contracts/v1). Local-agent,
MCPHub, and other harnesses can use it to detect compatibility drift without importing Cortex's
internal Go packages.

Compatibility jobs should fetch that directory from an exact Cortex release tag or commit rather
than a moving `main` ref. `contractVersion` selects the wrapper semantics; the Git ref binds the
exact fixture revision being tested.

## What v1 covers

The manifest groups 27 fixtures:

- all eight core lifecycle successes: `open_task`, `investigate`, `plan`, `begin_change`, `verify`,
  `status`, `remember`, and `handoff`;
- eight structural rejections covering disproof, boundary, lease ownership, no-diff, immutable
  acceptance, exact claim statements, and honest completion;
- eight degraded or bounded states, including unavailable tools, blocked command execution, stale
  receipts, unknown scope, pending decisions, bounded handoffs, and atomic proof-closure overflow;
- MCP success/rejection parity between JSON text and `structuredContent`, plus a real stdio
  handshake and clean-shutdown fixture.

Each fixture declares its wrapper version, generating Go test, canonical or illustrative status,
sensitive-data policy, and size behavior. Payloads are generated from the same kernel, handoff, and
MCP result paths used at runtime. IDs, timestamps, Git digests, and temporary paths are normalized;
raw tool output and secrets are excluded.

## Compatibility rule

Consumers must reject unknown future `contractVersion` values. They must not silently parse a v2
fixture as v1. Additive payload fields within a supported fixture version follow the compatibility
policy of the corresponding CLI/MCP result; structural gate outcomes and `isError` semantics are
canonical.

Run the corpus checks with the normal Go suite. For an intentional public contract change:

```bash
CORTEX_UPDATE_CONTRACTS=1 go test ./internal/kernel ./internal/mcp
go test ./...
```

Review every golden diff. Regeneration is a compatibility decision, not a formatting step.
