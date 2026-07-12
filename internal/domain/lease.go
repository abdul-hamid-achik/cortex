package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ChangeLease is a time-bounded ownership claim for change work. It lives in
// the case snapshot so optimistic concurrency makes competing acquisitions
// mutually exclusive across agents and processes.
type ChangeLease struct {
	Actor      string     `json:"actor"`
	AcquiredAt time.Time  `json:"acquiredAt"`
	RenewedAt  time.Time  `json:"renewedAt"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	ReleasedAt *time.Time `json:"releasedAt,omitempty"`
}

// Validate checks the lease's durable invariants.
func (l ChangeLease) Validate() error {
	if strings.TrimSpace(l.Actor) == "" {
		return errors.New("change lease needs an actor")
	}
	if l.AcquiredAt.IsZero() || l.RenewedAt.IsZero() || l.ExpiresAt.IsZero() {
		return errors.New("change lease needs acquired, renewed, and expiry timestamps")
	}
	if l.RenewedAt.Before(l.AcquiredAt) {
		return errors.New("change lease renewal cannot precede acquisition")
	}
	if !l.ExpiresAt.After(l.RenewedAt) {
		return errors.New("change lease expiry must follow its latest renewal")
	}
	if l.ReleasedAt != nil && l.ReleasedAt.Before(l.RenewedAt) {
		return errors.New("change lease release cannot precede its latest renewal")
	}
	return nil
}

// Active reports whether the lease still owns change work at now.
func (l ChangeLease) Active(now time.Time) bool {
	return l.ReleasedAt == nil && now.Before(l.ExpiresAt)
}

// Expired reports whether an unreleased lease has reached its deadline. An
// expired lease is stale and may be replaced by a new actor.
func (l ChangeLease) Expired(now time.Time) bool {
	return l.ReleasedAt == nil && !now.Before(l.ExpiresAt)
}

// Renew extends an active lease for the same actor.
func (l *ChangeLease) Renew(actor string, now time.Time, ttl time.Duration) error {
	if l == nil {
		return errors.New("change lease does not exist")
	}
	if actor != l.Actor {
		return fmt.Errorf("change lease belongs to %q", l.Actor)
	}
	if !l.Active(now) {
		return errors.New("change lease is not active")
	}
	next := *l
	next.RenewedAt = now
	next.ExpiresAt = now.Add(ttl)
	if err := next.Validate(); err != nil {
		return err
	}
	*l = next
	return nil
}

// Release relinquishes a lease for its owner. Releasing twice is idempotent for
// the same actor, and an owner may explicitly clean up its expired lease.
func (l *ChangeLease) Release(actor string, now time.Time) error {
	if l == nil {
		return errors.New("change lease does not exist")
	}
	if actor != l.Actor {
		return fmt.Errorf("change lease belongs to %q", l.Actor)
	}
	if l.ReleasedAt != nil {
		return nil
	}
	released := now
	next := *l
	next.ReleasedAt = &released
	if err := next.Validate(); err != nil {
		return err
	}
	*l = next
	return nil
}
