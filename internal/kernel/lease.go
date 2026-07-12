package kernel

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

const (
	DefaultChangeLeaseTTL = 15 * time.Minute
	MaxChangeLeaseTTL     = time.Hour
	minChangeLeaseTTL     = time.Second
	maxLeaseCASAttempts   = 5
)

// ChangeLeaseInput identifies the case, owner, and requested lease duration.
// A zero TTL uses DefaultChangeLeaseTTL.
type ChangeLeaseInput struct {
	TaskID string
	Actor  string
	TTL    time.Duration
}

// ReleaseChangeLeaseInput identifies the lease owner relinquishing change
// work. Actor matching prevents one active agent from releasing another's
// lease.
type ReleaseChangeLeaseInput struct {
	TaskID string
	Actor  string
}

// AcquireChangeLease claims bounded ownership of a planned change. A released
// or expired lease is stale and can be replaced; an active lease cannot.
func (k *Kernel) AcquireChangeLease(in ChangeLeaseInput) (domain.Envelope, error) {
	actor, err := k.changeLeaseActor(in.Actor)
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	ttl, err := normalizeChangeLeaseTTL(in.TTL)
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	now := k.now().UTC()
	c, err := k.updateChangeLease(in.TaskID, func(c *domain.CaseFile) error {
		if err := validateChangeLeaseCase(c); err != nil {
			return err
		}
		if c.ChangeLease != nil && c.ChangeLease.Active(now) {
			return leaseRuleError(fmt.Sprintf(
				"change lease is held by %q until %s", c.ChangeLease.Actor, c.ChangeLease.ExpiresAt.Format(time.RFC3339),
			))
		}
		lease := &domain.ChangeLease{
			Actor: actor, AcquiredAt: now, RenewedAt: now, ExpiresAt: now.Add(ttl),
		}
		if err := lease.Validate(); err != nil {
			return leaseRuleError(err.Error())
		}
		c.ChangeLease = lease
		return nil
	})
	if err != nil {
		return k.changeLeaseError(in.TaskID, err)
	}
	return k.leaseEnvelope(c, fmt.Sprintf("change lease acquired by %s until %s", actor, c.ChangeLease.ExpiresAt.Format(time.RFC3339))), nil
}

// RenewChangeLease extends an unexpired lease owned by the same actor. Once a
// lease expires it must be reacquired, which makes stale-owner recovery
// explicit and prevents an old worker from reviving itself after replacement.
func (k *Kernel) RenewChangeLease(in ChangeLeaseInput) (domain.Envelope, error) {
	actor, err := k.changeLeaseActor(in.Actor)
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	ttl, err := normalizeChangeLeaseTTL(in.TTL)
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	now := k.now().UTC()
	c, err := k.updateChangeLease(in.TaskID, func(c *domain.CaseFile) error {
		if err := validateChangeLeaseCase(c); err != nil {
			return err
		}
		if c.ChangeLease == nil {
			return leaseRuleError("change lease does not exist")
		}
		if c.ChangeLease.Expired(now) {
			return leaseRuleError("change lease expired; acquire a new lease")
		}
		if c.ChangeLease.ReleasedAt != nil {
			return leaseRuleError("change lease was released; acquire a new lease")
		}
		if err := c.ChangeLease.Renew(actor, now, ttl); err != nil {
			return leaseRuleError(err.Error())
		}
		return nil
	})
	if err != nil {
		return k.changeLeaseError(in.TaskID, err)
	}
	return k.leaseEnvelope(c, fmt.Sprintf("change lease renewed by %s until %s", actor, c.ChangeLease.ExpiresAt.Format(time.RFC3339))), nil
}

// ReleaseChangeLease marks the owner's lease released. The released record is
// retained in case.json until another actor acquires the case, preserving who
// last owned change work without keeping the task locked.
func (k *Kernel) ReleaseChangeLease(in ReleaseChangeLeaseInput) (domain.Envelope, error) {
	actor, err := k.changeLeaseActor(in.Actor)
	if err != nil {
		return errEnvelope(in.TaskID, err.Error()), nil
	}
	now := k.now().UTC()
	c, err := k.updateChangeLease(in.TaskID, func(c *domain.CaseFile) error {
		if c.ChangeLease == nil {
			return leaseRuleError("change lease does not exist")
		}
		if err := c.ChangeLease.Release(actor, now); err != nil {
			return leaseRuleError(err.Error())
		}
		return nil
	})
	if err != nil {
		return k.changeLeaseError(in.TaskID, err)
	}
	return k.leaseEnvelope(c, fmt.Sprintf("change lease released by %s", actor)), nil
}

// updateChangeLease applies a small case-snapshot mutation with bounded CAS
// retries. A competing successful update is always reloaded before the
// mutation is reconsidered, so two agents cannot both acquire an empty lease.
func (k *Kernel) updateChangeLease(taskID string, mutate func(*domain.CaseFile) error) (*domain.CaseFile, error) {
	var conflict error
	for attempt := 0; attempt < maxLeaseCASAttempts; attempt++ {
		c, err := k.store.Load(taskID)
		if err != nil {
			return nil, err
		}
		if err := mutate(c); err != nil {
			return c, err
		}
		if err := k.store.Save(c); err != nil {
			if errors.Is(err, casefs.ErrRevisionConflict) {
				conflict = err
				continue
			}
			return c, err
		}
		return c, nil
	}
	return nil, conflict
}

func validateChangeLeaseCase(c *domain.CaseFile) error {
	if c.Mode != domain.ModeChange {
		return leaseRuleError("change leases are only available for change tasks")
	}
	if c.Status.IsTerminal() || c.Status == domain.PhaseNeedsHumanDecision {
		return leaseRuleError(fmt.Sprintf("cannot own change work in phase %q", c.Status))
	}
	switch c.Status {
	case domain.PhasePlanned, domain.PhaseChanging, domain.PhaseVerifying:
		return nil
	default:
		return leaseRuleError(fmt.Sprintf("change work cannot begin in phase %q; plan the task first", c.Status))
	}
}

func normalizeChangeLeaseTTL(ttl time.Duration) (time.Duration, error) {
	if ttl == 0 {
		return DefaultChangeLeaseTTL, nil
	}
	if ttl < minChangeLeaseTTL || ttl > MaxChangeLeaseTTL {
		return 0, fmt.Errorf("change lease ttl must be between %s and %s", minChangeLeaseTTL, MaxChangeLeaseTTL)
	}
	return ttl, nil
}

func (k *Kernel) changeLeaseActor(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("change lease needs an actor")
	}
	if k.red.Detected(raw) {
		return "", errors.New("change lease actor must be a stable non-sensitive identifier")
	}
	if !validActorIdentifier(raw) {
		return "", errors.New("change lease actor may contain only letters, digits, dash, underscore, dot, slash, colon, or at-sign")
	}
	return k.red.String(raw), nil
}

type leaseRuleError string

func (e leaseRuleError) Error() string { return string(e) }

func (k *Kernel) changeLeaseError(taskID string, err error) (domain.Envelope, error) {
	var rule leaseRuleError
	if errors.As(err, &rule) || errors.Is(err, casefs.ErrNotFound) {
		return errEnvelope(taskID, err.Error()), nil
	}
	return errEnvelope(taskID, err.Error()), err
}

func (k *Kernel) leaseEnvelope(c *domain.CaseFile, summary string) domain.Envelope {
	env := domain.Envelope{
		OK: true, TaskID: c.ID, Phase: c.Status, Summary: summary,
		NextActions: nextForPhase(c.Status),
	}
	k.attachStructuredActions(&env, c)
	return env
}
