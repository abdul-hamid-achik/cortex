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
		return k.errEnvelopeForCase(c, fmt.Sprintf("cannot record an observation in terminal phase %q", c.Status)), nil
	}
	claim := strings.TrimSpace(in.Claim)
	if claim == "" {
		return k.errEnvelopeActions(in.TaskID, "observation needs a claim", domain.NextAction{
			Tool: "cortex_note", Command: cortexCommand(c, "note", c.ID, "CLAIM"),
			Reason: "state the observation, decision, or constraint to record", Arguments: knownActionArgs(c), Inputs: []string{"claim"},
		}), nil
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
		return k.errEnvelopeActions(in.TaskID, "observation category must be observation, decision, constraint, or handoff",
			noteEnumAction(c, "category", []string{"observation", "decision", "constraint", "handoff"})), nil
	}
	origin := strings.ToLower(strings.TrimSpace(in.Origin))
	if origin == "" {
		origin = "human"
	}
	switch origin {
	case "human", "agent", "reviewer":
	default:
		return k.errEnvelopeActions(in.TaskID, "observation origin must be human, agent, or reviewer",
			noteEnumAction(c, "origin", []string{"human", "agent", "reviewer"})), nil
	}
	confidence := domain.Confidence(strings.ToLower(strings.TrimSpace(in.Confidence)))
	if confidence == "" {
		confidence = domain.ConfidenceMedium
	}
	if confidence != domain.ConfidenceLow && confidence != domain.ConfidenceMedium {
		return k.errEnvelopeActions(in.TaskID, "observation confidence must be low or medium",
			noteEnumAction(c, "confidence", []string{"low", "medium"})), nil
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

// noteEnumAction offers a retryable cortex_note continuation naming which
// enum field was invalid, with the field's fixed, already-documented
// vocabulary as Candidates — the identical literal set the rejection's prose
// already names, never invented.
func noteEnumAction(c *domain.CaseFile, field string, values []string) domain.NextAction {
	return domain.NextAction{
		Tool: "cortex_note", Command: cortexCommand(c, "note", c.ID, "CLAIM", "--"+noteFlagName(field), values[0]),
		Reason: "use a valid value for " + field, Arguments: knownActionArgs(c),
		Inputs: []string{field}, Candidates: map[string][]string{field: values},
	}
}

// noteFlagName maps an ObservationInput field name to its cortex note CLI
// flag (category is exposed as --kind, matching cmd/cortex/note.go).
func noteFlagName(field string) string {
	if field == "category" {
		return "kind"
	}
	return field
}
