package eval

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
)

// Scenarios is the benchmark set. All eight are authored. Three run with
// no external tooling (verified outcome, honest degradation, candidate-not-proof);
// the other five drive a live specialist tool and self-skip via Requires when it
// is absent. Every scenario scores a correct OUTCOME and an adequate evidence
// trail — for the degraded cases the correct outcome IS honest degradation.
func Scenarios() []Scenario {
	return []Scenario{
		{Name: "known-symbol bug fix", Category: "known-symbol", Run: scnKnownSymbol},
		{Name: "stale / absent code index", Category: "stale-index", Run: scnStaleIndex},
		{Name: "misleading semantic search", Category: "misleading-search", Run: scnMisleadingSearch},

		{Name: "vague UI bug (browser)", Category: "vague-ui", Requires: []string{"cairn"}, Run: scnBrowser},
		{Name: "terminal/TUI regression", Category: "terminal-regression", Requires: []string{"glyph"}, Run: scnTerminal},
		{Name: "old artifact/video investigation", Category: "video", Requires: []string{"vidtrace"}, Run: scnVideo},
		{Name: "secret-backed local integration", Category: "secret", Requires: []string{"tvault"}, Run: scnSecret},
		{Name: "safe refactor with broad impact", Category: "refactor", Requires: []string{"codemap"}, Run: scnRefactor},
	}
}

// scnKnownSymbol checks that a known-symbol fix completes VERIFIED with an
// adequate evidence trail and no scope drift, given a passing structural review.
func scnKnownSymbol(t *testing.T) []string {
	env := NewEnv(t, map[string]string{
		"src/auth/callback.go": "package auth\nfunc HandleCallback(){}\n",
	}, codemapPass())
	k, ctx := env.Kernel(), env.Ctx()
	var f []string

	start, _ := k.StartTask(ctx, kernel.StartInput{Goal: "fix returnTo in HandleCallback", Surfaces: []domain.Surface{domain.SurfaceCode}})
	id := start.TaskID
	_, _ = k.Investigate(ctx, kernel.InvestigateInput{TaskID: id, Question: "HandleCallback"})
	plan, _ := k.Plan(kernel.PlanInput{TaskID: id,
		Hypotheses:     []kernel.HypothesisInput{{Statement: "returnTo dropped in HandleCallback", DisproveBy: "structural review of the diff"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/auth/callback.go"}, Symbols: []string{"HandleCallback"}},
		Uncertainty:    "unsure whether the encoder also strips it"})
	if !plan.OK {
		return append(f, "plan rejected: "+plan.Error)
	}
	env.Write("src/auth/callback.go", "package auth\nfunc HandleCallback(){ _ = 1 }\n")
	vr, _ := k.Verify(ctx, kernel.VerifyInput{TaskID: id, Claims: []string{"the callback preserves the return url"}})
	if !vr.OK {
		f = append(f, "verify failed: "+vr.Error)
	}
	rem, _ := k.Remember(ctx, kernel.RememberInput{TaskID: id, Outcome: "returnTo restored in HandleCallback"})
	if rem.Phase != domain.PhaseComplete {
		f = append(f, "task did not complete: "+string(rem.Phase))
	}

	// Evidence-trail adequacy requires both a correct outcome and adequate proof.
	m := env.Metrics(id)
	if m.EvidenceItems == 0 {
		f = append(f, "no evidence recorded")
	}
	if m.ScopeDrifted {
		f = append(f, "unexpected scope drift on an in-boundary edit")
	}
	// The code claim's structural review passed, so the code surface is verified.
	if !contains(m.VerifiedSurfaces, "code") {
		f = append(f, "code surface should be verified by the passing review")
	}
	return f
}

// scnStaleIndex checks that without an indexed structural tool Cortex degrades
// honestly — never fabricate a passing verification, and refuse to complete as
// "verified" without an explicit unverified acknowledgment.
func scnStaleIndex(t *testing.T) []string {
	// No codemap/vecgrep adapters registered → they are "unavailable" everywhere.
	env := NewEnv(t, map[string]string{"src/x.go": "package x\nfunc F(){}\n"})
	k, ctx := env.Kernel(), env.Ctx()
	var f []string

	start, _ := k.StartTask(ctx, kernel.StartInput{Goal: "change F", Surfaces: []domain.Surface{domain.SurfaceCode}})
	id := start.TaskID
	_, _ = k.Investigate(ctx, kernel.InvestigateInput{TaskID: id, Question: "F"})
	_, _ = k.Plan(kernel.PlanInput{TaskID: id,
		Hypotheses:     []kernel.HypothesisInput{{Statement: "F needs a guard", DisproveBy: "review the diff"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/x.go"}}, Uncertainty: "u"})
	env.Write("src/x.go", "package x\nfunc F(){ _ = 1 }\n")
	_, _ = k.Verify(ctx, kernel.VerifyInput{TaskID: id})

	// Completion WITHOUT acknowledgment must be refused (no real verification).
	blocked, _ := k.Remember(ctx, kernel.RememberInput{TaskID: id, Outcome: "guarded F"})
	if blocked.OK {
		return append(f, "completed despite no real verification — a fabricated 'done'")
	}
	// With an explicit acknowledgment it completes, but is NOT verified.
	ack, _ := k.Remember(ctx, kernel.RememberInput{TaskID: id, Outcome: "guarded F", VerificationNotPossible: true})
	if ack.Phase != domain.PhaseComplete {
		f = append(f, "explicit-unverified completion should succeed")
	}
	m := env.Metrics(id)
	if m.Verified {
		f = append(f, "an unverified task must not be reported as verified")
	}
	return f
}

// scnMisleadingSearch checks that a plausible-but-wrong search hit is
// recorded as a low-confidence CANDIDATE and must not, on its own, confirm a
// hypothesis (candidate ≠ proof).
func scnMisleadingSearch(t *testing.T) []string {
	env := NewEnv(t, map[string]string{
		"src/real.go":      "package src\nfunc Target(){}\n",
		"src/unrelated.go": "package src\nfunc Helper(){}\n",
	}, vecgrepMisleading())
	k, ctx := env.Kernel(), env.Ctx()
	var f []string

	start, _ := k.StartTask(ctx, kernel.StartInput{Goal: "find where Target is handled", Surfaces: []domain.Surface{domain.SurfaceCode}})
	id := start.TaskID
	inv, _ := k.Investigate(ctx, kernel.InvestigateInput{TaskID: id, Question: "where is Target handled"})

	// The misleading hit must be present but LOW confidence (a candidate).
	sawCandidate, highConfSearch := false, false
	for _, fact := range inv.Facts {
		if fact.Kind == "semantic_search" {
			sawCandidate = true
			if fact.Confidence == "high" {
				highConfSearch = true
			}
		}
	}
	if !sawCandidate {
		f = append(f, "the search candidate was not recorded")
	}
	if highConfSearch {
		f = append(f, "a semantic-search hit was recorded as high confidence (candidate rendered as proof)")
	}
	// Nothing verified it, so the hypothesis stays unresolved — cortex must not
	// auto-confirm from a search hit.
	_, _ = k.Plan(kernel.PlanInput{TaskID: id,
		Hypotheses:     []kernel.HypothesisInput{{Statement: "Target lives in unrelated.go", DisproveBy: "read the file"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/unrelated.go"}}, Uncertainty: "u"})
	m := env.Metrics(id)
	if m.UnresolvedHypotheses == 0 {
		f = append(f, "a hypothesis was resolved with no verifying evidence")
	}
	return f
}

// scnBrowser checks that a vague UI bug is treated as a browser-surface change. With no
// cairn spec covering it, cortex must NOT report the browser surface verified —
// an unproven browser claim stays unverified, never a fabricated pass.
func scnBrowser(t *testing.T) []string {
	env := NewEnv(t, map[string]string{"src/ui.go": "package ui\nfunc Render(){}\n"},
		adapters.NewCairntrace(), adapters.NewCodemap())
	return browserOrTerminalGuarantee(t, env, domain.SurfaceBrowser, "the login button does nothing when clicked", "the login button redirects to checkout", "browser", "cairntrace_flow")
}

// scnTerminal checks that a terminal/TUI regression is a terminal-surface change;
// same guarantee via glyph.
func scnTerminal(t *testing.T) []string {
	env := NewEnv(t, map[string]string{"cmd/app/main.go": "package main\nfunc main(){}\n"},
		adapters.NewGlyphrun(), adapters.NewCodemap())
	return browserOrTerminalGuarantee(t, env, domain.SurfaceTerminal, "the CLI prints the wrong exit banner", "the command exits 0 with the right banner", "terminal", "glyphrun_flow")
}

// browserOrTerminalGuarantee drives a behavioral-surface change through the loop
// and asserts (a) the behavioral verifier is genuinely REQUIRED but unmet — so
// the scenario really exercises the gap, not a trivial absence — and (b) the
// surface is never reported verified, and the task isn't reported verified.
func browserOrTerminalGuarantee(t *testing.T, env *Env, surface domain.Surface, question, claim, surfWord, verifier string) []string {
	k, ctx := env.Kernel(), env.Ctx()
	var f []string
	file := "src/ui.go"
	if surface == domain.SurfaceTerminal {
		file = "cmd/app/main.go"
	}
	start, _ := k.StartTask(ctx, kernel.StartInput{Goal: "fix " + surfWord + " regression",
		Surfaces: []domain.Surface{domain.SurfaceCode, surface}})
	id := start.TaskID
	_, _ = k.Investigate(ctx, kernel.InvestigateInput{TaskID: id, Question: question})
	_, _ = k.Plan(kernel.PlanInput{TaskID: id,
		Hypotheses:     []kernel.HypothesisInput{{Statement: "handler is wrong", DisproveBy: "run the " + surfWord + " flow"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{file}}, Uncertainty: "u"})
	env.Write(file, "package "+pkgFor(file)+"\nfunc "+funcFor(file)+"(){ _ = 1 }\n")
	// No spec covers the change (bare repo), so the behavioral claim cannot be
	// proven — it must NOT be reported as verified.
	_, _ = k.Verify(ctx, kernel.VerifyInput{TaskID: id, Claims: []string{claim}})
	m := env.Metrics(id)
	// The behavioral verifier must be REQUIRED (proving the scenario exercised the
	// surface) yet UNMET, and the surface must not be reported verified.
	if !contains(m.MissingVerification, verifier) {
		f = append(f, "expected "+verifier+" to be a required-but-unmet verifier; missing=["+strings.Join(m.MissingVerification, ",")+"]")
	}
	if contains(m.VerifiedSurfaces, string(surface)) {
		f = append(f, "the "+surfWord+" surface was reported verified with no passing "+surfWord+" run")
	}
	// Completing without a real behavioral verifier requires an explicit
	// acknowledgment (the code review alone doesn't verify the surface).
	rem, _ := k.Remember(ctx, kernel.RememberInput{TaskID: id, Outcome: "adjusted the handler", VerificationNotPossible: true})
	if rem.Phase != domain.PhaseComplete {
		f = append(f, "explicit-unverified completion should succeed: "+rem.Error)
	}
	if env.Metrics(id).Verified {
		f = append(f, "a "+surfWord+"-unverified task must not be reported as verified")
	}
	return f
}

// scnVideo checks that an old-artifact/video investigation with an invalid bundle
// must degrade honestly — a partial with the reason, never a fabricated
// "video failure likely owned by …" code-owner claim.
func scnVideo(t *testing.T) []string {
	env := NewEnv(t, map[string]string{"src/checkout.go": "package src\nfunc Checkout(){}\n"}, adapters.NewVidtrace())
	k, ctx := env.Kernel(), env.Ctx()
	var f []string
	start, _ := k.StartTask(ctx, kernel.StartInput{Goal: "investigate the checkout bug video", Surfaces: []domain.Surface{domain.SurfaceCode}})
	inv, _ := k.Investigate(ctx, kernel.InvestigateInput{TaskID: start.TaskID,
		Question: "the checkout page hangs", Video: "vidtrace://__no_such_stash_eval__"})
	// The vidtrace step must actually have RUN and reported the failure honestly
	// (proving the scenario exercised the tool, not a trivial pass).
	if !hasSubstr(inv.Warnings, "vidtrace") {
		f = append(f, "the invalid bug video did not surface a vidtrace warning — the step didn't run or didn't degrade honestly")
	}
	// …and it must not have fabricated an owning-code claim from a missing bundle.
	for _, fact := range inv.Facts {
		if strings.Contains(fact.Claim, "video failure likely owned by") {
			f = append(f, "an invalid bug video fabricated an owning-code claim: "+fact.Claim)
		}
	}
	return f
}

// scnSecret checks that a secret-backed task keeps secret VALUES out of the
// evidence ledger — tvault answers names/capability only, and a secret literal in
// a human-supplied reason is masked at the write boundary.
func scnSecret(t *testing.T) []string {
	env := NewEnv(t, map[string]string{"src/pay.go": "package src\nfunc Pay(){}\n"}, adapters.NewTvault())
	k, ctx := env.Kernel(), env.Ctx()
	var f []string
	start, _ := k.StartTask(ctx, kernel.StartInput{Goal: "wire the payment secret", Surfaces: []domain.Surface{domain.SurfaceSecret, domain.SurfaceCode}})
	id := start.TaskID
	_, _ = k.Investigate(ctx, kernel.InvestigateInput{TaskID: id, Question: "where is the stripe api key read"})
	plan, _ := k.Plan(kernel.PlanInput{TaskID: id,
		Hypotheses:     []kernel.HypothesisInput{{Statement: "the key is read in Pay", DisproveBy: "grep the readers"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/pay.go"}}, Uncertainty: "u"})
	// A human-supplied reason carrying a secret literal must be redacted before it
	// lands in an evidence record.
	const secret = "ghp_" + "16C7e42F292c6912E7710c838347Ae178B4a99"
	_, _ = k.Resolve(kernel.ResolveInput{TaskID: id, HypothesisID: firstHypID(plan),
		Status: "challenged", Reason: "the leaked token was " + secret + " in the logs"})
	evs, _ := k.Store().Evidence(id)
	redacted := false
	for _, ev := range evs {
		if strings.Contains(ev.Claim, secret) || strings.Contains(ev.Source.URI, secret) {
			f = append(f, "a secret value leaked into an evidence record: "+ev.ID)
		}
		if strings.Contains(ev.Claim, "«redacted»") {
			redacted = true // the resolution reason was masked, proving redaction ran
		}
	}
	if !redacted {
		f = append(f, "expected the secret-bearing resolution reason to be recorded, masked — redaction may not have run on the write path")
	}
	return f
}

// scnRefactor checks that a safe refactor with broad impact is high-risk; Cortex
// must NOT wave it through without a passing structural review — the escalation gate
// fires when the review is inconclusive.
func scnRefactor(t *testing.T) []string {
	env := NewEnv(t, map[string]string{"src/core.go": "package src\nfunc Hub(){}\n"}, adapters.NewCodemap())
	k, ctx := env.Kernel(), env.Ctx()
	var f []string
	start, _ := k.StartTask(ctx, kernel.StartInput{Goal: "rename the Hub API", Risk: "high", Surfaces: []domain.Surface{domain.SurfaceCode}})
	id := start.TaskID
	_, _ = k.Investigate(ctx, kernel.InvestigateInput{TaskID: id, Question: "Hub"})
	_, _ = k.Plan(kernel.PlanInput{TaskID: id,
		Hypotheses:     []kernel.HypothesisInput{{Statement: "Hub can be renamed safely", DisproveBy: "review the blast radius"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"src/core.go"}, Symbols: []string{"Hub"}}, Uncertainty: "u"})
	env.Write("src/core.go", "package src\nfunc Hub2(){ _ = 1 }\n")
	vr, _ := k.Verify(ctx, kernel.VerifyInput{TaskID: id, Claims: []string{"the rename is safe"}})
	// A high-risk change whose structural review didn't pass must be flagged
	// never silently accepted.
	if !hasSubstr(vr.Warnings, "high-risk change requires") {
		f = append(f, "a high-risk refactor with an unpassed review did not trigger escalation")
	}
	if contains(env.Metrics(id).VerifiedSurfaces, "code") {
		f = append(f, "a code surface with an inconclusive review was reported verified")
	}
	return f
}

func pkgFor(file string) string {
	if strings.HasPrefix(file, "cmd/") {
		return "main"
	}
	return "ui"
}
func funcFor(file string) string {
	if strings.HasPrefix(file, "cmd/") {
		return "main"
	}
	return "Render"
}
func firstHypID(env domain.Envelope) string {
	if len(env.Hypotheses) > 0 {
		return env.Hypotheses[0].ID
	}
	return ""
}
func hasSubstr(xs []string, sub string) bool {
	for _, x := range xs {
		if strings.Contains(x, sub) {
			return true
		}
	}
	return false
}

func contains(xs []string, x string) bool {
	for _, e := range xs {
		if e == x {
			return true
		}
	}
	return false
}
