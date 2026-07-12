package kernel

import (
	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/store/redact"
)

// redactSessionView is the last-line model/UI output filter for legacy case
// files and hand-edited ledgers that predate write-boundary redaction.
func redactSessionView(view *SessionView) {
	if view == nil || view.Case == nil {
		return
	}
	r := redact.New(config.For(view.Case.Workspace.Root).RedactLiterals...)
	c := view.Case
	c.Goal = r.String(c.Goal)
	c.Actor = r.String(c.Actor)
	c.IdempotencyKey = r.String(c.IdempotencyKey)
	c.BlockedReason = r.String(c.BlockedReason)
	c.Workspace.Root = r.String(c.Workspace.Root)
	c.Workspace.Repository = r.String(c.Workspace.Repository)
	c.Workspace.Branch = r.String(c.Workspace.Branch)
	c.Workspace.BaseRef = r.String(c.Workspace.BaseRef)
	c.Notes = redactSlice(r, c.Notes)
	redactBoundary(r, &c.ChangeBoundary)
	if view.Plan != nil {
		view.Plan.Uncertainty = r.String(view.Plan.Uncertainty)
		redactBoundary(r, &view.Plan.ChangeBoundary)
		redactHypotheses(r, view.Plan.Hypotheses)
	}
	redactHypotheses(r, view.Hypotheses)
	for i := range view.Evidence {
		evidence := &view.Evidence[i]
		evidence.Claim = r.String(evidence.Claim)
		evidence.Source.URI = r.String(evidence.Source.URI)
		evidence.Source.Actor = r.String(evidence.Source.Actor)
		if evidence.Location != nil {
			evidence.Location.File = r.String(evidence.Location.File)
			evidence.Location.Symbol = r.String(evidence.Location.Symbol)
		}
	}
	for i := range view.Receipts {
		receipt := &view.Receipts[i]
		receipt.Claim = r.String(receipt.Claim)
		receipt.Actor = r.String(receipt.Actor)
		receipt.Contract = r.String(receipt.Contract)
		receipt.Artifact = r.String(receipt.Artifact)
		receipt.Notes = r.String(receipt.Notes)
	}
	for i := range view.Decisions {
		redactDecision(r, &view.Decisions[i])
	}
	view.VerificationAssessment.MissingRequired = redactSlice(r, view.VerificationAssessment.MissingRequired)
	view.VerificationAssessment.NonPassingClaims = redactSlice(r, view.VerificationAssessment.NonPassingClaims)
	view.VerificationAssessment.FailedClaims = redactSlice(r, view.VerificationAssessment.FailedClaims)
	view.StaleVerification = redactSlice(r, view.StaleVerification)
	view.VerificationWarnings = redactSlice(r, view.VerificationWarnings)
	view.ProjectionWarnings = redactSlice(r, view.ProjectionWarnings)
	for i := range view.Timeline {
		view.Timeline[i].Summary = r.String(view.Timeline[i].Summary)
		view.Timeline[i].Detail = r.String(view.Timeline[i].Detail)
		view.Timeline[i].Ref = r.String(view.Timeline[i].Ref)
	}
	for i := range view.Actions {
		view.Actions[i].Command = r.String(view.Actions[i].Command)
		view.Actions[i].Reason = r.String(view.Actions[i].Reason)
		view.Actions[i].Inputs = redactSlice(r, view.Actions[i].Inputs)
		view.Actions[i].BlockedBy = redactSlice(r, view.Actions[i].BlockedBy)
		for key, value := range view.Actions[i].Arguments {
			if text, ok := value.(string); ok {
				view.Actions[i].Arguments[key] = r.String(text)
			}
		}
	}
}

func redactDecision(r *redact.Redactor, decision *domain.Decision) {
	if decision == nil {
		return
	}
	decision.Question = r.String(decision.Question)
	decision.Requester = r.String(decision.Requester)
	decision.Responder = r.String(decision.Responder)
	for j := range decision.Options {
		decision.Options[j].Label = r.String(decision.Options[j].Label)
		decision.Options[j].Consequence = r.String(decision.Options[j].Consequence)
	}
}

func redactBoundary(r *redact.Redactor, boundary *domain.ChangeBoundary) {
	if boundary == nil {
		return
	}
	boundary.Files = redactSlice(r, boundary.Files)
	boundary.Symbols = redactSlice(r, boundary.Symbols)
	boundary.Reason = r.String(boundary.Reason)
}

func redactHypotheses(r *redact.Redactor, hypotheses []domain.Hypothesis) {
	for i := range hypotheses {
		hypotheses[i].Statement = r.String(hypotheses[i].Statement)
		hypotheses[i].DisproveBy.Kind = r.String(hypotheses[i].DisproveBy.Kind)
		hypotheses[i].DisproveBy.Tool = r.String(hypotheses[i].DisproveBy.Tool)
		hypotheses[i].DisproveBy.Contract = r.String(hypotheses[i].DisproveBy.Contract)
		hypotheses[i].DisproveBy.Note = r.String(hypotheses[i].DisproveBy.Note)
	}
}

func redactSlice(r *redact.Redactor, values []string) []string {
	for i := range values {
		values[i] = r.String(values[i])
	}
	return values
}
