<script setup lang="ts">
import { withBase } from 'vitepress'
</script>

<template>
  <main class="cortex-home">
    <section class="home-hero" aria-labelledby="home-title">
      <div class="home-hero-copy">
        <p class="home-eyebrow">
          <span aria-hidden="true"></span>Local-first kernel for coding agents
        </p>
        <h1 id="home-title">Make every change <span>carry its proof.</span></h1>
        <p class="home-lead">
          Cortex sits between coding agents and their tools. It preserves the case, bounds the
          change, and binds verification to the exact repository state—so “done” never outruns
          evidence.
        </p>
        <div class="home-actions">
          <a class="home-button home-button-brand" :href="withBase('/quick-start')">
            Run your first case
          </a>
          <a class="home-button home-button-alt" href="#reasoning-loop">
            See the reasoning loop
          </a>
        </div>
        <a class="home-quiet-link" :href="withBase('/mcp')">
          Connecting an MCP agent? Read the MCP guide <span aria-hidden="true">→</span>
        </a>
      </div>

      <figure class="case-hero">
        <img
          :src="withBase('/hero-reasoning-loop.svg')"
          alt="The Cortex reasoning loop connecting orientation, evidence, planning, change, verification, and memory"
          width="620"
          height="520"
        >
        <figcaption>
          One durable case carries the work from orientation through verified, remembered proof.
        </figcaption>
      </figure>
    </section>

    <dl class="invariant-strip" aria-label="Cortex at a glance">
      <div>
        <dt>Local state</dt>
        <dd>Human-readable case files</dd>
      </div>
      <div>
        <dt>Bounded writer</dt>
        <dd>Lease plus declared scope</dd>
      </div>
      <div>
        <dt>Revision-bound proof</dt>
        <dd>Receipts bound to HEAD + diff</dd>
      </div>
      <div>
        <dt>Honest outcomes</dt>
        <dd>Verified · partial · failed · unverified</dd>
      </div>
    </dl>

    <section class="problem-section home-section" aria-labelledby="problem-title">
      <header class="section-heading">
        <p class="section-kicker">Why a kernel</p>
        <h2 id="problem-title">Coding is not the hard part. Keeping the reasoning intact is.</h2>
        <p>
          A capable model can write the function. The fragile part is preserving why one theory
          won, what would disprove it, who may edit which files, and whether the visible behavior
          actually passed.
        </p>
      </header>

      <div class="failure-ledger" role="list" aria-label="Common agent failure modes">
        <div class="failure-row" role="listitem">
          <span>01</span><p>Search hits quietly become confident causes.</p>
        </div>
        <div class="failure-row" role="listitem">
          <span>02</span><p>Two agents drift into the same change.</p>
        </div>
        <div class="failure-row" role="listitem">
          <span>03</span><p>A green build substitutes for browser proof.</p>
        </div>
        <div class="failure-row" role="listitem">
          <span>04</span><p>Context loss erases why the decision was made.</p>
        </div>
      </div>

      <p class="problem-resolution">
        Cortex makes each failure mode visible in durable state and stops the workflow when
        required structure is missing. The model remains the planner and author; the kernel keeps
        the work honest, recoverable, and inspectable.
      </p>
    </section>

    <section
      id="reasoning-loop"
      class="kernel-panel home-section"
      aria-labelledby="loop-title"
    >
      <div class="kernel-panel-copy">
        <div>
          <p class="section-kicker section-kicker-dark">The reasoning loop</p>
          <h2 id="loop-title">One case. Six guarded moves.</h2>
          <p>
            Each guarded move operates on explicit state and leaves a durable artifact. Skipped
            disproof paths, undeclared boundaries, competing writers, and stale receipts become
            data—not forgotten instructions.
          </p>
        </div>
        <a class="home-button home-button-on-dark" :href="withBase('/concepts')">
          Understand the kernel
        </a>
      </div>

      <ol class="loop-list" aria-label="Cortex workflow">
        <li>
          <span class="loop-index loop-index-done">01</span>
          <div><strong>Open</strong><span>workspace identity + baseline</span></div>
        </li>
        <li>
          <span class="loop-index loop-index-done">02</span>
          <div><strong>Investigate</strong><span>candidates with provenance</span></div>
        </li>
        <li>
          <span class="loop-index loop-index-done">03</span>
          <div><strong>Plan</strong><span>hypothesis + disproof + boundary</span></div>
        </li>
        <li>
          <span class="loop-index loop-index-done">04</span>
          <div><strong>Begin change</strong><span>bounded actor lease</span></div>
        </li>
        <li>
          <span class="loop-index loop-index-active">05</span>
          <div><strong>Verify</strong><span>exact contract + bound receipt</span></div>
        </li>
        <li>
          <span class="loop-index">06</span>
          <div><strong>Remember</strong><span>honest outcome + reusable memory</span></div>
        </li>
      </ol>
    </section>

    <section class="gates-section home-section" aria-labelledby="gates-title">
      <header class="section-heading section-heading-split">
        <div>
          <p class="section-kicker">What Cortex enforces</p>
          <h2 id="gates-title">Common agent failure modes become enforceable rules.</h2>
        </div>
        <p>
          These are state-machine gates and evidence policies, not suggestions in a prompt.
          Missing structure blocks progress; uncertain evidence stays visibly uncertain.
        </p>
      </header>

      <div class="gate-matrix" role="table" aria-label="Cortex rules and durable artifacts">
        <div class="gate-row gate-header" role="row">
          <span role="columnheader">Failure mode</span>
          <span role="columnheader">Kernel rule</span>
          <span role="columnheader">Durable artifact</span>
        </div>
        <div class="gate-row" role="row">
          <span role="cell"><small>Failure mode</small>Search output is mistaken for proof</span>
          <span role="cell"><small>Kernel rule</small>Candidate until structurally supported</span>
          <code role="cell"><small>Durable artifact</small>evidence record</code>
        </div>
        <div class="gate-row" role="row">
          <span role="cell"><small>Failure mode</small>A theory cannot be challenged</span>
          <span role="cell"><small>Kernel rule</small>Disproof path required</span>
          <code role="cell"><small>Durable artifact</small>plan + hypotheses</code>
        </div>
        <div class="gate-row" role="row">
          <span role="cell"><small>Failure mode</small>Two agents write concurrently</span>
          <span role="cell"><small>Kernel rule</small>Bounded lease plus revision CAS</span>
          <code role="cell"><small>Durable artifact</small>lease + revision</code>
        </div>
        <div class="gate-row" role="row">
          <span role="cell"><small>Failure mode</small>Tests ran against stale work</span>
          <span role="cell"><small>Kernel rule</small>HEAD and diff-bound verification</span>
          <code role="cell"><small>Durable artifact</small>verification receipt</code>
        </div>
        <div class="gate-row" role="row">
          <span role="cell"><small>Failure mode</small>A non-green result disappears</span>
          <span role="cell"><small>Kernel rule</small>Explicit completion outcome</span>
          <code role="cell"><small>Durable artifact</small>canonical assessment</code>
        </div>
      </div>

      <div class="assessment-band">
        <div>
          <p class="section-kicker">No unverified “done”</p>
          <h3>See exactly why a task is—or is not—done.</h3>
          <p>
            Missing proof stays visible instead of being rounded up to success. CLI, MCP, Studio,
            handoffs, and completion all read the same assessment.
          </p>
        </div>
        <ul class="status-list" aria-label="Canonical verification states">
          <li class="status-verified"><span aria-hidden="true"></span>verified</li>
          <li class="status-partial"><span aria-hidden="true"></span>partial</li>
          <li class="status-failed"><span aria-hidden="true"></span>failed</li>
          <li class="status-unverified"><span aria-hidden="true"></span>unverified</li>
        </ul>
        <a class="home-text-link" :href="withBase('/case-file')">
          Explore the case file <span aria-hidden="true">→</span>
        </a>
      </div>
    </section>

    <section class="surfaces-section home-section" aria-labelledby="surfaces-title">
      <header class="section-heading section-heading-split">
        <div>
          <p class="section-kicker">One kernel, three surfaces</p>
          <h2 id="surfaces-title">No forked truth between agents and people.</h2>
        </div>
        <p>
          Every surface works from the same case file, revision, decisions, evidence, and canonical
          verification assessment.
        </p>
      </header>

      <div class="surface-board">
        <div class="surface-core" aria-label="Shared Cortex state">
          <span>Same case · same revision</span>
          <strong>one canonical assessment</strong>
        </div>

        <a class="surface-row" :href="withBase('/mcp')">
          <span class="surface-index">01</span>
          <div>
            <span class="surface-audience">Agents</span>
            <h3>MCP</h3>
            <p>A compact lifecycle with structured next actions and bounded context.</p>
          </div>
          <code>cortex_open_task</code>
          <span class="surface-arrow" aria-hidden="true">→</span>
        </a>
        <a class="surface-row" :href="withBase('/cli')">
          <span class="surface-index">02</span>
          <div>
            <span class="surface-audience">Scripts + developers</span>
            <h3>JSON CLI</h3>
            <p>Human commands and stable machine output over the same kernel.</p>
          </div>
          <code>cortex status --json</code>
          <span class="surface-arrow" aria-hidden="true">→</span>
        </a>
        <a class="surface-row" :href="withBase('/studio')">
          <span class="surface-index">03</span>
          <div>
            <span class="surface-audience">Human operators</span>
            <h3>Studio</h3>
            <p>A live, read-only board of sessions across repositories.</p>
          </div>
          <code>cortex studio</code>
          <span class="surface-arrow" aria-hidden="true">→</span>
        </a>
      </div>
    </section>

    <section class="install-section home-section" aria-labelledby="install-title">
      <div class="install-copy">
        <p class="section-kicker section-kicker-dark">Start locally</p>
        <h2 id="install-title">One binary. Your specialist tools stay optional.</h2>
        <p>
          Install Cortex once, then open a case in any Git repository or expose the compact MCP
          profile to your agent. Missing specialist tools degrade honestly—Cortex never fabricates
          their output.
        </p>
        <p class="install-note">Pure Go · Git required · local-first state · MIT licensed</p>
      </div>

      <div class="install-commands">
        <figure class="command-block">
          <figcaption><span>01</span> Install Cortex</figcaption>
          <pre><code><span class="prompt">$</span> go install github.com/abdul-hamid-achik/cortex/cmd/cortex@latest
<span class="prompt">$</span> cortex open "Fix checkout redirect" --actor agent-auth</code></pre>
        </figure>
        <figure class="command-block">
          <figcaption><span>02</span> Connect an MCP agent</figcaption>
          <pre><code><span class="prompt">$</span> mcphub add cortex cortex serve
<span class="prompt">$</span> mcphub sync --write</code></pre>
        </figure>
      </div>
    </section>

    <section class="closing-section home-section" aria-labelledby="closing-title">
      <div>
        <p class="section-kicker">Ready when your agent is</p>
        <h2 id="closing-title">Put the next agent change on the record.</h2>
      </div>
      <div>
        <p>
          Open a durable case, follow the six guarded moves, and preserve the evidence behind the
          outcome.
        </p>
        <div class="home-actions">
          <a class="home-button home-button-brand" :href="withBase('/quick-start')">
            Run your first case
          </a>
          <a class="home-button home-button-alt" :href="withBase('/tutorial')">
            Follow the full tutorial
          </a>
        </div>
      </div>
    </section>
  </main>
</template>
