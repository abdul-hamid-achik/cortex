package kernel

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/adapters"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

// structuredNextForCase returns executable continuation hints. It deliberately
// keeps the old prose NextActions intact for humans and older clients.
func structuredNextForCase(c *domain.CaseFile, assessments ...VerificationAssessment) []domain.NextAction {
	return structuredNextForCaseAt(c, time.Now().UTC(), assessments...)
}

func structuredNextForCaseAt(c *domain.CaseFile, now time.Time, assessments ...VerificationAssessment) []domain.NextAction {
	if c == nil {
		return nil
	}
	args := func() map[string]any { return knownActionArgs(c) }
	beginChange := func(reason string) domain.NextAction {
		ttl := DefaultChangeLeaseTTL.String()
		action := domain.NextAction{
			Tool: "cortex_begin_change", Command: cortexCommand(c, "begin-change", c.ID, "--ttl", ttl),
			Reason: reason, Arguments: args(), Inputs: []string{"actor"},
		}
		action.Arguments["ttl"] = ttl
		if c.ChangeLease != nil && c.ChangeLease.Active(now) {
			action.Arguments["actor"] = c.ChangeLease.Actor
			action.Command += " --actor " + shellArg(c.ChangeLease.Actor)
			action.Inputs = nil
		}
		return action
	}
	ownershipInactive := c.Mode == domain.ModeChange && c.ChangeLease != nil && !c.ChangeLease.Active(now)
	switch c.Status {
	case domain.PhaseNew, domain.PhaseOrienting:
		openArgs := args()
		delete(openArgs, "taskId")
		openArgs["goal"] = c.Goal
		openArgs["mode"] = string(c.Mode)
		openArgs["risk"] = c.Risk
		openArgs["surfaces"] = append([]domain.Surface(nil), c.Surfaces...)
		if c.Actor != "" {
			openArgs["actor"] = c.Actor
		}
		if c.ParentTaskID != "" {
			openArgs["parentTaskId"] = c.ParentTaskID
		}
		if c.IdempotencyKey != "" {
			openArgs["idempotencyKey"] = c.IdempotencyKey
		}
		commandTokens := []string{"open", c.Goal, "--mode", string(c.Mode), "--risk", c.Risk}
		for _, surface := range c.Surfaces {
			commandTokens = append(commandTokens, "--surface", string(surface))
		}
		if c.Actor != "" {
			commandTokens = append(commandTokens, "--actor", c.Actor)
		}
		if c.ParentTaskID != "" {
			commandTokens = append(commandTokens, "--parent", c.ParentTaskID)
		}
		if c.IdempotencyKey != "" {
			commandTokens = append(commandTokens, "--idempotency-key", c.IdempotencyKey)
		}
		return []domain.NextAction{{
			Tool: "cortex_open_task", Command: cortexCommand(c, commandTokens...),
			Reason: "finish interrupted orientation and resume the durable case", Arguments: openArgs,
		}}
	case domain.PhaseInvestigating:
		planInputs := []string{"hypotheses", "uncertainty"}
		if c.Mode == domain.ModeChange {
			planInputs = append(planInputs, "files")
		}
		investigate := domain.NextAction{
			Tool: "cortex_investigate", Command: cortexCommand(c, "investigate", c.ID),
			Reason: "gather evidence before choosing a change", Arguments: args(), Inputs: []string{"question"},
		}
		plan := domain.NextAction{
			Tool: "cortex_plan", Command: cortexCommand(c, "plan", c.ID),
			Reason: "declare hypotheses, disproof paths, boundary, and verification", Arguments: args(), Inputs: planInputs,
		}
		if c.InvestigationRounds > 0 {
			return []domain.NextAction{plan, investigate}
		}
		return []domain.NextAction{investigate, plan}
	case domain.PhasePlanned:
		if c.Mode == domain.ModeChange {
			return []domain.NextAction{beginChange("claim the bounded change before editing")}
		}
		return []domain.NextAction{{Tool: "cortex_verify", Command: cortexCommand(c, "verify", c.ID), Reason: "run the declared verification plan", Arguments: args()}}
	case domain.PhaseChanging:
		if ownershipInactive {
			return []domain.NextAction{beginChange("reacquire expired or released change ownership before editing or verification")}
		}
		verifyArgs := args()
		verifyCommand := cortexCommand(c, "verify", c.ID)
		if c.ChangeLease != nil && c.ChangeLease.Active(now) {
			verifyArgs["actor"] = c.ChangeLease.Actor
			verifyCommand += " --actor " + shellArg(c.ChangeLease.Actor)
		}
		return []domain.NextAction{{Tool: "cortex_verify", Command: verifyCommand, Reason: "prove the edited workspace and detect scope drift", Arguments: verifyArgs}}
	case domain.PhaseVerifying:
		assessment := VerificationAssessment{}
		if len(assessments) > 0 {
			assessment = assessments[0]
		}
		if c.Mode == domain.ModeChange && assessment.Outcome == VerificationFailed {
			return []domain.NextAction{beginChange("return to bounded change work to repair the failed verification")}
		}
		if ownershipInactive && assessment.Outcome != VerificationVerified {
			return []domain.NextAction{beginChange("reacquire expired or released change ownership before rerunning verification")}
		}
		verifyArgs := args()
		verifyCommand := cortexCommand(c, "verify", c.ID)
		if c.ChangeLease != nil && c.ChangeLease.Active(now) {
			verifyArgs["actor"] = c.ChangeLease.Actor
			verifyCommand += " --actor " + shellArg(c.ChangeLease.Actor)
		}
		remember := domain.NextAction{
			Tool: "cortex_remember", Command: cortexCommand(c, "remember", c.ID),
			Reason: "preserve the verified outcome, evidence, and uncertainty", Arguments: args(), Inputs: []string{"outcome"},
		}
		if assessment.Outcome == VerificationVerified {
			return []domain.NextAction{remember}
		}
		remember.Reason = "preserve the outcome once verification is adequate"
		return []domain.NextAction{
			{Tool: "cortex_verify", Command: verifyCommand, Reason: "rerun missing or stale verifiers", Arguments: verifyArgs},
			remember,
		}
	case domain.PhasePersisting:
		return []domain.NextAction{{Tool: "cortex_remember", Command: cortexCommand(c, "remember", c.ID), Reason: "finish durable preservation", Arguments: args(), Inputs: []string{"outcome"}}}
	case domain.PhaseNeedsHumanDecision:
		return []domain.NextAction{{
			Tool: "cortex_answer_decision", Command: cortexCommand(c, "decision", "answer", c.ID, "DECISION_ID"),
			Reason: "the case is paused until a human chooses one recorded option", Arguments: args(),
			Inputs: []string{"decisionId", "answer", "responder"}, BlockedBy: []string{"pending human decision"},
		}}
	default:
		return nil
	}
}

func knownActionArgs(c *domain.CaseFile) map[string]any {
	return map[string]any{"taskId": c.ID, "workspace": c.Workspace.Root}
}

// cortexCommand renders a workspace-pinned, shell-safe human continuation.
// Structured consumers should invoke Tool + Arguments; Command is deliberately
// safe to copy into a POSIX shell without allowing case text to expand `$`,
// backticks, quotes, or whitespace into new arguments.
func cortexCommand(c *domain.CaseFile, args ...string) string {
	parts := []string{"cortex"}
	if c != nil && strings.TrimSpace(c.Workspace.Root) != "" {
		parts = append(parts, "-C", shellArg(c.Workspace.Root))
	}
	for _, arg := range args {
		parts = append(parts, shellArg(arg))
	}
	return strings.Join(parts, " ")
}

func shellArg(value string) string {
	if value != "" {
		safe := true
		for i := 0; i < len(value); i++ {
			if !shellSafeByte(value[i]) {
				safe = false
				break
			}
		}
		if safe {
			return value
		}
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func shellSafeByte(ch byte) bool {
	if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' {
		return true
	}
	return strings.ContainsRune("_./:@%+=,-", rune(ch))
}

func (k *Kernel) attachStructuredActions(env *domain.Envelope, c *domain.CaseFile) {
	if env == nil {
		return
	}
	actions := structuredNextForCaseAt(c, k.now().UTC())
	if c != nil && c.Status == domain.PhaseVerifying {
		if receipts, err := k.store.Verifications(c.ID); err == nil {
			if !c.Status.IsTerminal() {
				var revisionErr error
				current := adapters.Revision{}
				if k.git == nil {
					revisionErr = errors.New("git adapter unavailable")
				} else {
					current, revisionErr = k.git.CurrentRevision(context.Background(), c.Workspace.Root)
				}
				receipts, _ = verificationReceiptsAtRevision(receipts, current, revisionErr)
			}
			actions = structuredNextForCaseAt(c, k.now().UTC(), assessCaseVerification(c, receipts))
		}
	}
	if c != nil {
		if decisions, err := k.store.Decisions(c.ID); err == nil {
			actions = hydrateDecisionActions(c, actions, decisions)
		}
	}
	env.Actions = k.redactStructuredActions(actions)
}

func (k *Kernel) redactStructuredActions(actions []domain.NextAction) []domain.NextAction {
	for i := range actions {
		actions[i].Command = k.red.String(actions[i].Command)
		actions[i].Reason = k.red.String(actions[i].Reason)
		actions[i].Inputs = k.redactStrings(actions[i].Inputs)
		actions[i].BlockedBy = k.redactStrings(actions[i].BlockedBy)
		for key, value := range actions[i].Arguments {
			if text, ok := value.(string); ok {
				actions[i].Arguments[key] = k.red.String(text)
			}
		}
	}
	return actions
}

// hydrateDecisionActions makes both normal and crash-recovery decision states
// directly invokable. A pending record whose case pause did not commit is
// repaired by retrying the original request; an answered record whose resume
// did not commit uses answer_decision's resume mode.
func hydrateDecisionActions(c *domain.CaseFile, actions []domain.NextAction, decisions []domain.Decision) []domain.NextAction {
	var pending *domain.Decision
	for i := len(decisions) - 1; i >= 0; i-- {
		if decisions[i].Status == domain.DecisionPending {
			decision := decisions[i]
			pending = &decision
			break
		}
	}
	if pending != nil {
		if c.Status == domain.PhaseNeedsHumanDecision {
			return bindPendingDecision(actions, decisions)
		}
		args := knownActionArgs(c)
		args["question"] = pending.Question
		args["options"] = append([]domain.DecisionOption(nil), pending.Options...)
		args["requester"] = pending.Requester
		command := []string{"decision", "request", c.ID, "--question", pending.Question, "--requester", pending.Requester}
		for _, option := range pending.Options {
			command = append(command, "--option", option.ID+"="+option.Label+"|"+option.Consequence)
		}
		return []domain.NextAction{{
			Tool: "cortex_request_decision", Command: cortexCommand(c, command...), Reason: "finish an interrupted decision pause using the durable request",
			Arguments: args,
		}}
	}
	if c.Status == domain.PhaseNeedsHumanDecision {
		for i := len(decisions) - 1; i >= 0; i-- {
			if decisions[i].Status != domain.DecisionAnswered {
				continue
			}
			args := knownActionArgs(c)
			args["resume"] = true
			return []domain.NextAction{{
				Tool: "cortex_answer_decision", Command: cortexCommand(c, "decision", "resume", c.ID),
				Reason: "finish resuming an answered decision after an interrupted case update", Arguments: args,
			}}
		}
	}
	return actions
}

func bindPendingDecision(actions []domain.NextAction, decisions []domain.Decision) []domain.NextAction {
	decisionID := ""
	for i := len(decisions) - 1; i >= 0; i-- {
		if decisions[i].Status == domain.DecisionPending {
			decisionID = decisions[i].ID
			break
		}
	}
	if decisionID == "" {
		return actions
	}
	for i := range actions {
		if actions[i].Tool != "cortex_answer_decision" {
			continue
		}
		if actions[i].Arguments == nil {
			actions[i].Arguments = map[string]any{}
		}
		actions[i].Arguments["decisionId"] = decisionID
		actions[i].Command = strings.Replace(actions[i].Command, "DECISION_ID", shellArg(decisionID), 1)
		var inputs []string
		for _, input := range actions[i].Inputs {
			if input != "decisionId" {
				inputs = append(inputs, input)
			}
		}
		actions[i].Inputs = inputs
	}
	return actions
}
