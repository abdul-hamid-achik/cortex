package kernel

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/ids"
)

// ObservationInput records context obtained outside a specialist adapter. It
// is deliberately human_report evidence, which domain policy never permits to
// satisfy verification by itself.
type ObservationInput struct {
	TaskID     string
	Claim      string
	Category   string // observation | decision | constraint | handoff
	Origin     string // human | agent | reviewer
	Actor      string
	URI        string
	Location   *domain.Location
	Confidence string // low | medium; prose-only notes cannot become high proof
	Sensitive  bool
}

// RecordObservation appends a redacted, provenance-bearing note to an active
// case. Terminal cases remain immutable so summaries and recall cannot diverge
// from their evidence after completion.
func (k *Kernel) RecordObservation(in ObservationInput) (domain.Envelope, error) {
	c, err := k.store.Load(in.TaskID)
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	if c.Status.IsTerminal() {
		return errEnvelope(in.TaskID, fmt.Sprintf("cannot record an observation in terminal phase %q", c.Status)), nil
	}
	claim := strings.TrimSpace(in.Claim)
	if claim == "" {
		return errEnvelope(in.TaskID, "observation needs a claim"), nil
	}
	if textExceeds(claim, maxRecordTextBytes) {
		return errEnvelope(in.TaskID, fmt.Sprintf("observation claim exceeds %d bytes", maxRecordTextBytes)), nil
	}
	if textExceeds(strings.TrimSpace(in.URI), maxLocatorBytes) {
		return errEnvelope(in.TaskID, fmt.Sprintf("observation uri exceeds %d bytes", maxLocatorBytes)), nil
	}
	if textExceeds(strings.TrimSpace(in.Actor), maxStableIdentifierBytes) {
		return errEnvelope(in.TaskID, fmt.Sprintf("observation actor exceeds %d bytes", maxStableIdentifierBytes)), nil
	}
	category := strings.ToLower(strings.TrimSpace(in.Category))
	if category == "" {
		category = "observation"
	}
	switch category {
	case "observation", "decision", "constraint", "handoff":
	default:
		return errEnvelope(in.TaskID, "observation category must be observation, decision, constraint, or handoff"), nil
	}
	origin := strings.ToLower(strings.TrimSpace(in.Origin))
	if origin == "" {
		origin = "human"
	}
	switch origin {
	case "human", "agent", "reviewer":
	default:
		return errEnvelope(in.TaskID, "observation origin must be human, agent, or reviewer"), nil
	}
	confidence := domain.Confidence(strings.ToLower(strings.TrimSpace(in.Confidence)))
	if confidence == "" {
		confidence = domain.ConfidenceMedium
	}
	if confidence != domain.ConfidenceLow && confidence != domain.ConfidenceMedium {
		return errEnvelope(in.TaskID, "observation confidence must be low or medium"), nil
	}
	redactedClaim := k.red.String(claim)
	redactedURI := k.red.String(in.URI)
	redactedActor := k.red.String(in.Actor)
	var location *domain.Location
	locationSensitive := false
	if in.Location != nil {
		location = &domain.Location{
			File: k.red.String(in.Location.File), StartLine: in.Location.StartLine,
			EndLine: in.Location.EndLine, Symbol: k.red.String(in.Location.Symbol),
		}
		locationSensitive = k.red.Detected(in.Location.File) || k.red.Detected(in.Location.Symbol)
	}
	sensitive := in.Sensitive || k.red.Detected(claim) || k.red.Detected(in.URI) || k.red.Detected(in.Actor) || locationSensitive
	id := ids.New("ev")
	ev := domain.Evidence{
		ID: id, Timestamp: k.now().UTC(), Kind: domain.KindHumanReport,
		Source: domain.Source{Origin: origin, Actor: redactedActor, URI: redactedURI},
		Claim:  redactedClaim, Category: category, Location: location,
		Confidence: confidence, Sensitivity: sensitivity(sensitive),
		RawRef: fmt.Sprintf("case://%s/evidence/%s", c.ID, id),
	}
	if err := k.store.AppendEvidence(c.ID, ev); err != nil {
		return errEnvelope(c.ID, err.Error()), err
	}
	env := domain.Envelope{
		OK: true, TaskID: c.ID, Phase: c.Status,
		Summary: fmt.Sprintf("recorded %s from %s", category, origin),
		Facts:   []domain.FactView{domain.ToFactView(ev)}, RawAvailable: false,
		NextActions: nextForPhase(c.Status),
	}
	k.attachStructuredActions(&env, c)
	return env, nil
}
