package kernel

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

// BeginChangeInput claims bounded ownership and makes the planned→changing
// transition explicit. A zero TTL uses the default lease duration.
type BeginChangeInput struct {
	TaskID string
	Actor  string
	TTL    time.Duration
}

// BeginChange is idempotent for the active lease owner and mutually exclusive
// across actors.
func (k *Kernel) BeginChange(in BeginChangeInput) (domain.Envelope, error) {
	actor, err := k.changeLeaseActor(in.Actor)
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	c, err := k.store.Load(in.TaskID)
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	if c.Mode != domain.ModeChange {
		return errEnvelope(c.ID, "begin-change is only valid for change tasks"), nil
	}
	if c.Status != domain.PhasePlanned && c.Status != domain.PhaseChanging && c.Status != domain.PhaseVerifying {
		return errEnvelope(c.ID, fmt.Sprintf("cannot begin change in phase %q; plan the task first", c.Status)), nil
	}
	if !c.ChangeBoundary.Declared() {
		return errEnvelope(c.ID, "cannot begin change without a declared boundary"), nil
	}
	now := k.now().UTC()
	if c.ChangeLease == nil || !c.ChangeLease.Active(now) {
		acquired, acquireErr := k.AcquireChangeLease(ChangeLeaseInput{TaskID: c.ID, Actor: actor, TTL: in.TTL})
		if acquireErr != nil {
			return acquired, acquireErr
		}
		if !acquired.OK {
			// Two identical begin-change calls can both observe an empty lease.
			// Acquire's CAS gives one the lease; the loser should converge on that
			// same-owner result rather than violating BeginChange's idempotency.
			latest, loadErr := k.store.Load(c.ID)
			if loadErr != nil {
				return errEnvelope(c.ID, loadErr.Error()), loadErr
			}
			if latest.ChangeLease == nil || !latest.ChangeLease.Active(k.now().UTC()) || latest.ChangeLease.Actor != actor {
				return acquired, nil
			}
		}
	} else if c.ChangeLease.Actor != actor {
		return errEnvelope(c.ID, fmt.Sprintf("change lease is held by %q until %s", c.ChangeLease.Actor, c.ChangeLease.ExpiresAt.Format(time.RFC3339))), nil
	} else if in.TTL != 0 {
		// A same-owner retry with an explicit TTL doubles as a heartbeat. This
		// keeps the minimal MCP surface sufficient for long-running changes.
		renewed, renewErr := k.RenewChangeLease(ChangeLeaseInput{TaskID: c.ID, Actor: actor, TTL: in.TTL})
		if renewErr != nil || !renewed.OK {
			return renewed, renewErr
		}
	}

	for attempt := 0; attempt < maxLeaseCASAttempts; attempt++ {
		c, err = k.store.Load(in.TaskID)
		if err != nil {
			return errEnvelope(in.TaskID, err.Error()), err
		}
		if c.ChangeLease == nil || !c.ChangeLease.Active(k.now().UTC()) || c.ChangeLease.Actor != actor {
			return errEnvelope(c.ID, "change ownership changed before the phase transition; acquire the lease again"), nil
		}
		if c.Status == domain.PhaseChanging {
			env := k.leaseEnvelope(c, fmt.Sprintf("change already in progress under %s", actor))
			k.attachStructuredActions(&env, c)
			return env, nil
		}
		if (c.Status != domain.PhasePlanned && c.Status != domain.PhaseVerifying) || !domain.CanTransition(c.Status, domain.PhaseChanging) {
			return errEnvelope(c.ID, fmt.Sprintf("cannot begin change in phase %q", c.Status)), nil
		}
		from := c.Status
		c.Status = domain.PhaseChanging
		if err = k.store.Save(c); err != nil {
			if errors.Is(err, casefs.ErrRevisionConflict) {
				continue
			}
			return errEnvelope(c.ID, err.Error()), err
		}
		k.recordPhase(c.ID, from, c.Status)
		summary := fmt.Sprintf("change begun by %s within the declared boundary", actor)
		if from == domain.PhaseVerifying {
			summary = fmt.Sprintf("verification repair begun by %s within the declared boundary", actor)
		}
		env := domain.Envelope{
			OK: true, TaskID: c.ID, Phase: c.Status,
			Summary:     summary,
			NextActions: []string{"make edits within the declared boundary", "renew the change lease before it expires", "cortex verify when the change is ready"},
		}
		k.attachStructuredActions(&env, c)
		return env, nil
	}
	return errEnvelope(in.TaskID, "case changed concurrently too many times; retry begin-change"), nil
}

func validateVerificationLease(c *domain.CaseFile, actor string, now time.Time) error {
	if c == nil || c.ChangeLease == nil {
		return nil
	}
	if c.ChangeLease.ReleasedAt != nil {
		return fmt.Errorf("change lease was released; reacquire with begin-change before verification")
	}
	if c.ChangeLease.Expired(now) {
		return fmt.Errorf("change lease expired; reacquire with begin-change before verification")
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return fmt.Errorf("active change lease belongs to %q; verify must name that actor", c.ChangeLease.Actor)
	}
	if actor != c.ChangeLease.Actor {
		return fmt.Errorf("active change lease belongs to %q, not %q", c.ChangeLease.Actor, actor)
	}
	return nil
}
