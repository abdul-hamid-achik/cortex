package kernel

import (
	"fmt"
	"strings"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/store/casefs"
)

// ChildStatus is a parent's view of one delegated case. Ledgers stay separate;
// this is a rollup, not a merge.
type ChildStatus struct {
	ID                  string              `json:"id"`
	Phase               domain.Phase        `json:"phase"`
	Mode                domain.Mode         `json:"mode"`
	Active              bool                `json:"active"`
	VerificationOutcome VerificationOutcome `json:"verificationOutcome"`
}

func (k *Kernel) childStatuses(ids []string) []ChildStatus {
	out := make([]ChildStatus, 0, len(ids))
	for _, id := range ids {
		child, store, err := k.loadChildCase(id)
		if err != nil || child == nil {
			out = append(out, ChildStatus{ID: id, Active: true, VerificationOutcome: VerificationUnverified})
			continue
		}
		receipts, _ := store.Verifications(child.ID)
		assessment := assessCaseVerification(child, receipts)
		out = append(out, ChildStatus{
			ID: child.ID, Phase: child.Status, Mode: child.Mode,
			Active: !child.Status.IsTerminal(), VerificationOutcome: assessment.Outcome,
		})
	}
	return out
}

func (k *Kernel) loadChildCase(id string) (*domain.CaseFile, *casefs.Store, error) {
	if c, err := k.store.Load(id); err == nil {
		return c, k.store, nil
	}
	_, store, err := LocateSession(id)
	if err != nil {
		return nil, nil, err
	}
	c, err := store.Load(id)
	if err != nil {
		return nil, nil, err
	}
	return c, store, nil
}

func (k *Kernel) refuseOpenChildren(c *domain.CaseFile, accept bool) error {
	if accept || len(c.ChildTaskIDs) == 0 {
		return nil
	}
	var open []string
	for _, child := range k.childStatuses(c.ChildTaskIDs) {
		if child.Active {
			open = append(open, child.ID)
		}
	}
	if len(open) == 0 {
		return nil
	}
	return fmt.Errorf("cannot complete: %d child task(s) still in-flight (%s); finish them or set accept_open_children=true",
		len(open), strings.Join(clipList(open, 5), ", "))
}
