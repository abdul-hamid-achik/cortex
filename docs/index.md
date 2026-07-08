---
layout: home

hero:
  name: Cortex
  text: An evidence-guided agent kernel
  tagline: A local-first runtime that gives software-engineering agents durable state, disciplined tool use, bounded changes, and verification tied to what a human actually sees.
  actions:
    - theme: brand
      text: Quick Start
      link: /quick-start
    - theme: alt
      text: Tutorial
      link: /tutorial
    - theme: alt
      text: Concepts
      link: /concepts
    - theme: alt
      text: View on GitHub
      link: https://github.com/abdul-hamid-achik/cortex

features:
  - title: A reasoning loop, enforced
    details: orient → investigate → plan → change → verify → preserve. The phase machine enforces it as structural invariants, not prompt suggestions.
  - title: Evidence, not assertions
    details: Every claim is backed by a locatable source with provenance and confidence. Search results are recorded as candidates, never as proof.
  - title: No unverified "done"
    details: A task cannot complete without a verification receipt. A claim with no verifier is recorded not_run — never rendered as passed.
  - title: Bounded changes
    details: Declare a change boundary before editing. Cortex flags scope drift so accidental expansion is visible instead of silent.
  - title: Secret-safe
    details: tvault is an execution boundary, not a content provider. Secret values never enter the model's context; a redactor is the last-line filter.
  - title: Composes your toolchain
    details: codemap, vecgrep, cairntrace, glyphrun, fcheap, tvault — all optional at runtime. Adapters degrade safely and never fabricate a missing tool's output.
---

## What is Cortex?

Cortex sits between an LLM and a set of specialist tools. It does not replace the model's
planning or coding ability — it supplies what models are consistently bad at preserving across
long tool-using tasks: **stable task state, explicit evidence, disciplined tool selection,
bounded changes, verification tied to user-visible behavior, durable memory, and secret-safe
execution.**

Instead of choosing among dozens of overlapping raw tools, the model uses **six cognitive
actions** — `start`, `investigate`, `plan`, `verify`, `remember`, `status` — and Cortex
translates each into the right specialist calls while keeping an evidence trail.

Two surfaces over one kernel: a **CLI** (with `--json` for agents) and an **MCP server**
(`cortex serve`).

> More tools without structure = more ways to get lost.
> Specialized tools **+ a kernel** = accumulated engineering judgment.
