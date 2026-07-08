package tui

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
)

func newTestKernel(t *testing.T) *kernel.Kernel {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@t.co"}, {"config", "user.name", "t"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	k, err := kernel.New(config.For(dir))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestBoardRendersEmpty(t *testing.T) {
	k := newTestKernel(t)
	m, err := newModel(k)
	if err != nil {
		t.Fatal(err)
	}
	out := m.render()
	if !strings.Contains(out, "Cortex studio") {
		t.Errorf("board title missing:\n%s", out)
	}
	if !strings.Contains(out, "no tasks yet") {
		t.Errorf("empty state missing:\n%s", out)
	}
}

func TestBoardRendersCaseDetail(t *testing.T) {
	k := newTestKernel(t)
	env, _ := k.StartTask(context.Background(), kernel.StartInput{Goal: "fix the redirect bug"})
	if !env.OK {
		t.Fatalf("start failed: %s", env.Error)
	}

	m, err := newModel(k)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(m.tasks))
	}
	out := m.render()
	if !strings.Contains(out, "fix the redirect bug") {
		t.Errorf("case goal missing from detail pane:\n%s", out)
	}
	if !strings.Contains(out, "Evidence") {
		t.Errorf("evidence section missing:\n%s", out)
	}
}

func TestBoardNavigationKeys(t *testing.T) {
	k := newTestKernel(t)
	for _, g := range []string{"task one", "task two"} {
		if _, err := k.StartTask(context.Background(), kernel.StartInput{Goal: g}); err != nil {
			t.Fatal(err)
		}
	}
	m, err := newModel(k)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(m.tasks))
	}

	// Down moves the cursor; quit returns tea.Quit.
	nm, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if nm.(model).cursor != 1 {
		t.Errorf("expected cursor 1 after down, got %d", nm.(model).cursor)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd == nil {
		t.Error("expected a quit command on 'q'")
	}
}

func TestBoardRendersFullCaseDetail(t *testing.T) {
	// Build a case with hypotheses + a verification receipt so the detail pane's
	// Hypotheses and Verification sections render (branches unit tests missed).
	k := newTestKernel(t)
	ctx := context.Background()
	env, _ := k.StartTask(ctx, kernel.StartInput{Goal: "fix the redirect", Surfaces: []domain.Surface{domain.SurfaceCode}})
	id := env.TaskID
	_, _ = k.Plan(kernel.PlanInput{TaskID: id,
		Hypotheses:     []kernel.HypothesisInput{{Statement: "returnTo dropped", DisproveBy: "review the diff"}},
		ChangeBoundary: domain.ChangeBoundary{Files: []string{"callback.go"}},
		Uncertainty:    "unsure",
	})
	_, _ = k.Verify(ctx, kernel.VerifyInput{TaskID: id, Claims: []string{"the code is sound"}})

	m, err := newModel(k)
	if err != nil {
		t.Fatal(err)
	}
	out := m.render()
	for _, want := range []string{"fix the redirect", "Hypotheses", "returnTo dropped", "Verification", "Evidence"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail pane missing %q:\n%s", want, out)
		}
	}
}

func TestBoardRefreshKey(t *testing.T) {
	k := newTestKernel(t)
	_, _ = k.StartTask(context.Background(), kernel.StartInput{Goal: "one"})
	m, _ := newModel(k)
	// Add a task after the model loaded, then press r to refresh.
	_, _ = k.StartTask(context.Background(), kernel.StartInput{Goal: "two"})
	nm, _ := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if len(nm.(model).tasks) != 2 {
		t.Errorf("r should refresh the task list to 2, got %d", len(nm.(model).tasks))
	}
}

func TestBoardJumpKeys(t *testing.T) {
	k := newTestKernel(t)
	for _, g := range []string{"a", "b", "c"} {
		_, _ = k.StartTask(context.Background(), kernel.StartInput{Goal: g})
	}
	m, _ := newModel(k)
	// G jumps to the last, g jumps to the first.
	end, _ := m.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	if end.(model).cursor != 2 {
		t.Errorf("G should jump to last (2), got %d", end.(model).cursor)
	}
	home, _ := end.(model).Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	if home.(model).cursor != 0 {
		t.Errorf("g should jump to first (0), got %d", home.(model).cursor)
	}
}

func TestBoardWindowResize(t *testing.T) {
	k := newTestKernel(t)
	m, _ := newModel(k)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
	if nm.(model).width != 200 || nm.(model).height != 60 {
		t.Errorf("resize not applied: %dx%d", nm.(model).width, nm.(model).height)
	}
	// render at the new size must not panic.
	_ = nm.(model).render()
}
