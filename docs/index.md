---
layout: home
title: Evidence-guided engineering agents

hero:
  name: Local-first agent kernel
  text: Make every change carry its proof.
  tagline: Cortex gives software-engineering agents durable state, explicit uncertainty, bounded changes, and verification tied to the workspace a human will actually use.
  image:
    src: /hero-kernel.svg
    alt: The Cortex reasoning loop orbiting a durable case file
  actions:
    - theme: brand
      text: Start with Cortex
      link: /quick-start
    - theme: alt
      text: Follow the full tutorial
      link: /tutorial
    - theme: alt
      text: Read the agent guide
      link: /agents

features:
  - title: A reasoning loop that survives context loss
    details: Open, investigate, plan, claim, verify, and remember. Durable case files and retry-safe identity preserve the thread across tools, agents, and interrupted responses.
  - title: Evidence before confidence
    details: Every important claim keeps a source, confidence band, actor, and provenance. Search output remains a candidate until structural or behavioral evidence supports it.
  - title: No unverified “done”
    details: One canonical verified / partial / failed / unverified assessment powers CLI, MCP, Studio, handoffs, and completion gates.
  - title: Bounded multi-agent change
    details: Declare files and symbols, claim an expiring actor lease, and let Cortex reject competing writers or surface scope drift before work is preserved.
  - title: Progressive context, not context flooding
    details: Compact facts lead to full evidence and then bounded raw previews. Agents spend tokens only when deeper material is actually needed.
  - title: Secret-safe by construction
    details: tvault proves capability without returning values, while write-boundary and output redaction keep secret-shaped text out of case files and model context.
  - title: One kernel, three views
    details: Agents use MCP, scripts use JSON CLI output, and people supervise every repository from the live Studio board—all over the same state machine.
---

<div class="home-intro">
  <div class="home-kicker">What Cortex changes</div>
  <div class="home-copy">
    <p>A strong model can write the function. The hard part is keeping <strong>why this theory won, what would disprove it, which files are allowed to move, and whether the visible behavior truly passed</strong> intact through a long task.</p>
    <p>Cortex turns those fragile thoughts into a local case file with enforceable gates. The model stays the planner and author; the kernel keeps the work honest, recoverable, and inspectable.</p>
  </div>
</div>

<section class="kernel-panel" aria-labelledby="loop-title">
  <div class="kernel-panel-copy">
    <div>
      <h2 id="loop-title">A small surface with a durable spine.</h2>
      <p>Each action advances one explicit state. Skipped disproof paths, undeclared change boundaries, competing owners, stale receipts, and unverifiable claims become data—not forgotten instructions.</p>
    </div>
    <a class="VPButton medium brand" href="/concepts">Understand the kernel</a>
  </div>
  <ol class="loop-list" aria-label="Cortex workflow">
    <li><span class="loop-index">01</span><span>open / orient</span></li>
    <li><span class="loop-index">02</span><span>investigate evidence</span></li>
    <li><span class="loop-index">03</span><span>plan + disproof</span></li>
    <li><span class="loop-index">04</span><span>claim bounded change</span></li>
    <li><span class="loop-index">05</span><span>verify current revision</span></li>
    <li><span class="loop-index">06</span><span>remember outcome</span></li>
  </ol>
</section>

## Useful at every model tier

<div class="agent-spectrum">
  <section class="agent-mode">
    <span class="agent-mode-label">Smaller agents</span>
    <h3>Less inference between calls.</h3>
    <p>A shared result envelope, explicit required inputs, executable next actions, closed enums, and honest tool degradation reduce the amount of protocol the model must reconstruct on its own.</p>
  </section>
  <section class="agent-mode">
    <span class="agent-mode-label">Powerful agents</span>
    <h3>More room for real reasoning.</h3>
    <p>Revision-bound receipts, cross-case disproof recall, multi-agent leases, bounded handoffs, typed claims, and full provenance preserve sophisticated work without forcing it into a simplistic autopilot.</p>
  </section>
</div>

<section class="install-panel" aria-labelledby="install-title">
  <div>
    <h2 id="install-title">Local in one command.</h2>
    <p>Install the pure-Go binary, open a case in any Git repository, and expose the compact MCP profile to your agent. Specialist tools are optional and degrade without fabricated output.</p>
  </div>
  <div class="terminal-window" aria-label="Cortex installation example">
    <div class="terminal-bar" aria-hidden="true"><i></i><i></i><i></i></div>
    <pre><span class="prompt">$</span> go install github.com/abdul-hamid-achik/cortex/cmd/cortex@latest
<span class="prompt">$</span> cortex open "Fix checkout redirect" --actor agent-auth
<span class="success">✓ [investigating]</span> task opened with durable evidence state
<span class="prompt">$</span> cortex serve
compact MCP profile ready</pre>
  </div>
</section>

> More tools without structure create more ways to get lost. Specialized tools plus a durable kernel create accumulated engineering judgment.
