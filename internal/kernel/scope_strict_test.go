package kernel

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestBoundaryDoesNotSuffixMatchDifferentDirectory(t *testing.T) {
	k := newTestKernel(t, testRepo(t))
	if !k.inBoundary("src/callback.go", []string{"src/callback.go"}) {
		t.Fatal("exact boundary should match")
	}
	if k.inBoundary("other/src/callback.go", []string{"src/callback.go"}) {
		t.Fatal("suffix matching hid an out-of-boundary file")
	}
	if !k.inBoundary("src/nested/callback.go", []string{"src/**"}) {
		t.Fatal("explicit recursive glob should match")
	}
}

func TestPlanCanonicalizesInsideAbsoluteBoundaryAndRejectsOutside(t *testing.T) {
	ws := testRepo(t)
	k := newTestKernel(t, ws)
	started, _ := k.StartTask(context.Background(), StartInput{Goal: "g"})
	planned, _ := k.Plan(PlanInput{
		TaskID: started.TaskID, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{filepath.Join(ws, "src", "callback.go")}}, Uncertainty: "u",
	})
	if !planned.OK {
		t.Fatalf("inside absolute boundary rejected: %+v", planned)
	}
	c, _ := k.Store().Load(started.TaskID)
	if len(c.ChangeBoundary.Files) != 1 || c.ChangeBoundary.Files[0] != "src/callback.go" {
		t.Fatalf("boundary was not canonicalized: %+v", c.ChangeBoundary)
	}

	other, _ := k.StartTask(context.Background(), StartInput{Goal: "g2"})
	rejected, _ := k.Plan(PlanInput{
		TaskID: other.TaskID, Hypotheses: []HypothesisInput{{Statement: "h", DisproveBy: "d"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{filepath.Join(filepath.Dir(ws), "outside.go")}}, Uncertainty: "u",
	})
	if rejected.OK {
		t.Fatalf("outside boundary accepted: %+v", rejected)
	}
}
