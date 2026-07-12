package domain

import (
	"testing"
	"time"
)

func TestChangeLeaseLifecycle(t *testing.T) {
	acquired := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	lease := ChangeLease{
		Actor: "agent-a", AcquiredAt: acquired, RenewedAt: acquired,
		ExpiresAt: acquired.Add(5 * time.Minute),
	}
	if err := lease.Validate(); err != nil {
		t.Fatalf("valid lease rejected: %v", err)
	}
	if !lease.Active(acquired.Add(time.Minute)) || lease.Expired(acquired.Add(time.Minute)) {
		t.Fatal("lease should be active before expiry")
	}
	if err := lease.Renew("agent-b", acquired.Add(time.Minute), 5*time.Minute); err == nil {
		t.Fatal("another actor renewed the lease")
	}
	if err := lease.Renew("agent-a", acquired.Add(time.Minute), 10*time.Minute); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if got := lease.ExpiresAt; !got.Equal(acquired.Add(11 * time.Minute)) {
		t.Fatalf("renewed expiry = %s", got)
	}
	if err := lease.Release("agent-a", acquired.Add(2*time.Minute)); err != nil {
		t.Fatalf("release: %v", err)
	}
	if lease.Active(acquired.Add(3*time.Minute)) || lease.Expired(acquired.Add(20*time.Minute)) {
		t.Fatal("released lease must be neither active nor stale")
	}
	if err := lease.Release("agent-a", acquired.Add(4*time.Minute)); err != nil {
		t.Fatalf("idempotent release: %v", err)
	}
}

func TestChangeLeaseExpires(t *testing.T) {
	now := time.Now().UTC()
	lease := ChangeLease{Actor: "agent", AcquiredAt: now, RenewedAt: now, ExpiresAt: now.Add(time.Second)}
	if !lease.Expired(now.Add(time.Second)) {
		t.Fatal("lease should be stale at its exact expiry")
	}
	if err := lease.Renew("agent", now.Add(time.Second), time.Minute); err == nil {
		t.Fatal("stale lease should require reacquisition")
	}
}

func TestChangeLeaseValidate(t *testing.T) {
	now := time.Now().UTC()
	tests := []ChangeLease{
		{},
		{Actor: "agent"},
		{Actor: "agent", AcquiredAt: now, RenewedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute)},
		{Actor: "agent", AcquiredAt: now, RenewedAt: now, ExpiresAt: now},
	}
	for i, lease := range tests {
		if err := lease.Validate(); err == nil {
			t.Errorf("invalid lease %d accepted: %+v", i, lease)
		}
	}
}
