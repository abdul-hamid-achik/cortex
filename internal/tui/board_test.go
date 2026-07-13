package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/abdul-hamid-achik/cortex/internal/config"
	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
)

// newTestKernel isolates global dirs and returns a kernel for a fresh repo, so a
// test can create sessions that the (global) board then reads back.
func newTestKernel(t *testing.T) *kernel.Kernel {
	t.Helper()
	// Isolate global dirs — cases default to $XDG_STATE_HOME/cortex now.
	t.Setenv("CORTEX_HOME", t.TempDir())
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

func allFilter() kernel.SessionFilter { return kernel.SessionFilter{} }

func newLoadedModel(t *testing.T, filter kernel.SessionFilter) model {
	t.Helper()
	m := newModel(filter)
	loaded, cmd := m.Update(m.sessionsCmd(m.refreshRequest, m.filter)())
	m = loaded.(model)
	if cmd != nil {
		loaded, next := m.Update(cmd())
		m = loaded.(model)
		if next != nil {
			t.Fatal("initial detail load unexpectedly scheduled more work")
		}
	}
	return m
}

func TestBoardRendersEmpty(t *testing.T) {
	_ = newTestKernel(t) // isolates CORTEX_HOME; no sessions started
	m := newLoadedModel(t, allFilter())
	out := m.render()
	if !strings.Contains(out, "Cortex studio") {
		t.Errorf("board title missing:\n%s", out)
	}
	if !strings.Contains(out, "no sessions yet") {
		t.Errorf("empty state missing:\n%s", out)
	}
	if !strings.Contains(out, `cortex open "<goal>"`) || strings.Contains(out, "cortex start") {
		t.Errorf("empty state should recommend the preferred open command:\n%s", out)
	}
}

func TestBoardRendersCaseDetail(t *testing.T) {
	k := newTestKernel(t)
	env, _ := k.StartTask(context.Background(), kernel.StartInput{Goal: "fix the redirect bug"})
	if !env.OK {
		t.Fatalf("start failed: %s", env.Error)
	}
	m := newLoadedModel(t, allFilter())
	if len(m.sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(m.sessions))
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
	m := newLoadedModel(t, allFilter())
	if len(m.sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(m.sessions))
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

func TestBoardSearchEditorIsModalUnicodeSafeAndBounded(t *testing.T) {
	a := kernel.SessionSummary{ID: "task_a", Slug: "billing", Goal: "repair redirect", Phase: domain.PhaseInvestigating}
	b := kernel.SessionSummary{ID: "task_b", Slug: "search", Goal: "other work", Phase: domain.PhasePlanned}
	source := &fakeBoardSource{
		sessionsFn: func(filter kernel.SessionFilter) ([]kernel.SessionSummary, error) {
			if filter.Query != "qarj cafX" {
				t.Fatalf("source query = %q, want normalized modal input", filter.Query)
			}
			return []kernel.SessionSummary{a}, nil
		},
		detailFn: func(_ string, taskID string) (kernel.SessionView, error) {
			return kernel.SessionView{Case: &domain.CaseFile{ID: taskID, Goal: "matching detail"}}, nil
		},
	}
	m := newModelWithSource(allFilter(), source)
	m.refreshInFlight = false
	m.refreshRequest = 0
	m.filterApplied = true
	m.appliedFilter = m.filter
	m.sessions, m.cursor = []kernel.SessionSummary{a, b}, 1

	opened, cmd := m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m = opened.(model)
	if cmd != nil || !m.searchEditing {
		t.Fatal("/ should enter search mode without starting I/O")
	}
	for _, text := range []string{"q", "a", "r", "j", " ", "café"} {
		updated, keyCmd := m.Update(tea.KeyPressMsg{Code: []rune(text)[0], Text: text})
		m = updated.(model)
		if keyCmd != nil {
			t.Fatalf("search text %q triggered a global command", text)
		}
	}
	if m.cursor != 1 || m.filter.ActiveOnly || m.refreshInFlight || m.searchDraft != "qarj café" {
		t.Fatalf("modal search leaked a global key action: %+v", m)
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyPressMsg{Text: "\x1b[31mX\n"})
	m = updated.(model)
	if m.searchDraft != "qarj cafX" || strings.Contains(m.searchDraft, "31m") {
		t.Fatalf("Unicode backspace/control sanitization = %q", m.searchDraft)
	}

	updated, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if cmd == nil || m.searchEditing || m.filter.Query != "qarj cafX" || source.sessionCalls != 0 {
		t.Fatalf("Enter did not schedule the normalized search asynchronously: %+v", m)
	}
	updated, _ = m.Update(cmd())
	m = updated.(model)
	if !m.filterApplied || m.appliedFilter.Query != "qarj cafX" || len(m.sessions) != 1 || m.sessions[0].ID != a.ID {
		t.Fatalf("search result was not applied: %+v", m)
	}

	if got := []rune(appendSearchText("", strings.Repeat("界", maxSearchRunes+20))); len(got) != maxSearchRunes {
		t.Fatalf("interactive search retained %d runes, want %d", len(got), maxSearchRunes)
	}
}

func TestBoardSearchEscapeCancelsAndControlCStillQuits(t *testing.T) {
	m := model{filter: kernel.SessionFilter{Query: "kept"}, searchDraft: "kept", width: 50, height: 12}
	opened, _ := m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m = opened.(model)
	changed, _ := m.Update(tea.KeyPressMsg{Text: " changed"})
	m = changed.(model)
	cancelled, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = cancelled.(model)
	if cmd != nil || m.searchEditing || m.searchDraft != "kept" || m.filter.Query != "kept" {
		t.Fatalf("Escape did not restore the applied draft: %+v", m)
	}
	opened, _ = m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	_, cmd = opened.(model).Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl-C should quit even while search owns other keys")
	}
}

func TestBoardSearchEmptyAndPendingFilterStatesAreHonest(t *testing.T) {
	queryFilter := kernel.SessionFilter{Query: "billing failed"}
	m := model{
		filter: queryFilter, appliedFilter: queryFilter, filterApplied: true,
		width: 60, height: 14, lastRefresh: time.Now(),
	}
	out := ansi.Strip(m.render())
	for _, want := range []string{"0 sessions", "no sessions match", `"billing failed"`, "c clear", "r refresh"} {
		if !strings.Contains(out, want) {
			t.Errorf("search empty state missing %q:\n%s", want, out)
		}
	}

	m.source = &fakeBoardSource{
		sessionsFn: func(kernel.SessionFilter) ([]kernel.SessionSummary, error) { return nil, errors.New("offline") },
		detailFn:   func(string, string) (kernel.SessionView, error) { return kernel.SessionView{}, nil },
	}
	cleared, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	m = cleared.(model)
	if cmd == nil || m.filter.Query != "" || !strings.Contains(ansi.Strip(m.render()), "applying filters") {
		t.Fatalf("clear did not preserve and label the previous snapshot while pending: %+v", m)
	}
	failed, _ := m.Update(cmd())
	m = failed.(model)
	out = ansi.Strip(m.render())
	if !strings.Contains(out, "requested filters not applied") || !strings.Contains(out, `"billing failed"`) {
		t.Fatalf("failed filter mislabeled the retained snapshot:\n%s", out)
	}
	assertTerminalBounds(t, m.render(), m.width, m.height)
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
	_, _ = k.Verify(ctx, kernel.VerifyInput{TaskID: id, Claims: []string{"the code is sound"}, NoOpAcknowledged: true})

	m := newLoadedModel(t, allFilter())
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
	m := newLoadedModel(t, allFilter())
	// Add a session after the model loaded, then press r to refresh.
	_, _ = k.StartTask(context.Background(), kernel.StartInput{Goal: "two"})
	nm, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if cmd == nil || len(nm.(model).sessions) != 1 {
		t.Fatal("r should schedule, not synchronously perform, a refresh")
	}
	refreshed, _ := nm.(model).Update(cmd())
	if len(refreshed.(model).sessions) != 2 {
		t.Errorf("r refresh should load 2 sessions, got %d", len(refreshed.(model).sessions))
	}
}

func TestBoardAutoRefreshTick(t *testing.T) {
	k := newTestKernel(t)
	_, _ = k.StartTask(context.Background(), kernel.StartInput{Goal: "one"})
	m := newLoadedModel(t, allFilter())
	_, _ = k.StartTask(context.Background(), kernel.StartInput{Goal: "two"})
	nm, cmd := m.Update(tickMsg(time.Now()))
	if len(nm.(model).sessions) != 1 || !nm.(model).refreshInFlight {
		t.Error("tick should schedule a refresh without blocking to apply it")
	}
	if cmd == nil {
		t.Error("tick should reschedule the next tick")
	}
}

func TestBoardActiveFilterToggle(t *testing.T) {
	k := newTestKernel(t)
	ctx := context.Background()
	d, _ := k.StartTask(ctx, kernel.StartInput{Goal: "to abort"})
	if _, err := k.AbortTask(d.TaskID, "not needed"); err != nil {
		t.Fatal(err)
	}
	_, _ = k.StartTask(ctx, kernel.StartInput{Goal: "still going"})

	m := newLoadedModel(t, allFilter())
	if len(m.sessions) != 2 {
		t.Fatalf("expected 2 sessions before filter, got %d", len(m.sessions))
	}
	nm, cmd := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	filtered, _ := nm.(model).Update(cmd())
	if got := len(filtered.(model).sessions); got != 1 {
		t.Errorf("active-only should show 1 in-flight session, got %d", got)
	}
}

func TestBoardJumpKeys(t *testing.T) {
	k := newTestKernel(t)
	for _, g := range []string{"a", "b", "c"} {
		_, _ = k.StartTask(context.Background(), kernel.StartInput{Goal: g})
	}
	m := newLoadedModel(t, allFilter())
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
	_ = newTestKernel(t)
	m := newLoadedModel(t, allFilter())
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
	if nm.(model).width != 200 || nm.(model).height != 60 {
		t.Errorf("resize not applied: %dx%d", nm.(model).width, nm.(model).height)
	}
	// render at the new size must not panic.
	_ = nm.(model).render()
}

func TestBoardMarksStaleSession(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	m := model{
		sessions: []kernel.SessionSummary{
			{ID: "task_x", Slug: "repo", Goal: "forgotten work", Phase: domain.PhaseInvestigating, Active: true, UpdatedAt: old},
		},
		width: 100, height: 20,
	}
	if out := m.renderList(10, 40); !strings.Contains(out, "⚠") {
		t.Errorf("a stale in-flight session should be marked ⚠ in the list, got:\n%s", out)
	}
	if out := m.render(); !strings.Contains(out, "stale") {
		t.Errorf("the header should report the stale count, got:\n%s", out)
	}
}

func TestLoopStepper(t *testing.T) {
	if s := loopStepper(domain.PhaseInvestigating); !strings.Contains(s, "[inv]") {
		t.Errorf("investigating should highlight [inv]: %s", s)
	}
	if s := loopStepper(domain.PhaseComplete); !strings.Contains(s, "✓") {
		t.Errorf("complete should show ✓: %s", s)
	}
	if s := loopStepper(domain.PhaseBlocked); !strings.Contains(s, "blocked") {
		t.Errorf("a terminal-bad phase should show a stop marker: %s", s)
	}
}

func TestBoardRenderingStaysWithinTerminalBounds(t *testing.T) {
	hyps := make([]domain.Hypothesis, 0, 40)
	for i := range 40 {
		hyps = append(hyps, domain.Hypothesis{Statement: "hypothesis with a deliberately long explanation " + strings.Repeat("x", i+10)})
	}
	view := kernel.SessionView{
		Case: &domain.CaseFile{
			ID: "task_bounds", Goal: "界面 safety across narrow and wide terminals", Status: domain.PhaseInvestigating,
			Mode: domain.ModeChange, Risk: "medium", Workspace: domain.Workspace{Repository: "cortex", CommitBefore: strings.Repeat("a", 40)},
		},
		Hypotheses:    hyps,
		Evidence:      []domain.Evidence{{Claim: "tail evidence", Confidence: domain.ConfidenceHigh}},
		EvidenceTotal: 241,
	}
	for _, size := range []struct {
		width, height int
		wantNarrow    bool
	}{{40, 12, true}, {60, 20, true}, {80, 24, false}, {120, 40, false}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m := model{
				sessions: []kernel.SessionSummary{{ID: "task_bounds", Slug: "cortex", Goal: view.Case.Goal, Phase: view.Case.Status}},
				detail:   detail{loaded: true, view: view}, width: size.width, height: size.height,
			}
			if got := m.boardLayout().narrow; got != size.wantNarrow {
				t.Fatalf("narrow layout = %v, want %v", got, size.wantNarrow)
			}
			assertTerminalBounds(t, m.render(), size.width, size.height)
		})
	}
}

func TestBoardFramesRemainCompleteAtTerminalBounds(t *testing.T) {
	view := kernel.SessionView{Case: &domain.CaseFile{ID: "task_frame", Goal: "frame", Status: domain.PhaseInvestigating}}
	for _, tc := range []struct {
		name          string
		width, height int
		wide          bool
	}{
		{name: "wide", width: 100, height: 20, wide: true},
		{name: "narrow", width: 60, height: 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := model{
				sessions: []kernel.SessionSummary{{ID: "task_frame", Slug: "repo", Goal: "frame", Phase: domain.PhaseInvestigating}},
				detail:   detail{loaded: true, view: view}, width: tc.width, height: tc.height,
			}
			lines := strings.Split(ansi.Strip(m.render()), "\n")
			if len(lines) != tc.height || !strings.Contains(lines[len(lines)-1], "sessions") {
				t.Fatalf("render did not retain its complete body and help line (%d lines):\n%s", len(lines), strings.Join(lines, "\n"))
			}
			bodyTop, bodyBottom := lines[1], lines[len(lines)-2]
			if tc.wide {
				if strings.Count(bodyTop, "╭") != 2 || strings.Count(bodyTop, "╮") != 2 ||
					strings.Count(bodyBottom, "╰") != 2 || strings.Count(bodyBottom, "╯") != 2 {
					t.Fatalf("wide pane borders were clipped:\ntop: %q\nbottom: %q", bodyTop, bodyBottom)
				}
			} else {
				layout := m.boardLayout()
				listBottom := lines[layout.listHeight]
				detailTop := lines[layout.listHeight+1]
				if !strings.HasPrefix(bodyTop, "╭") || !strings.HasSuffix(listBottom, "╯") ||
					!strings.HasPrefix(detailTop, "╭") || !strings.HasSuffix(bodyBottom, "╯") {
					t.Fatalf("narrow pane borders were clipped:\n%s", strings.Join(lines, "\n"))
				}
			}
		})
	}
}

func TestBoardDetailScrollReachesLongContent(t *testing.T) {
	hyps := make([]domain.Hypothesis, 0, 30)
	for i := range 30 {
		hyps = append(hyps, domain.Hypothesis{Statement: fmt.Sprintf("hypothesis-%02d", i)})
	}
	view := kernel.SessionView{
		Case:          &domain.CaseFile{ID: "task_scroll", Goal: "scroll the case", Status: domain.PhaseInvestigating},
		Hypotheses:    hyps,
		Evidence:      []domain.Evidence{{Claim: "tail evidence is reachable", Confidence: domain.ConfidenceHigh}},
		EvidenceTotal: 1,
	}
	m := model{
		sessions: []kernel.SessionSummary{{ID: "task_scroll", Slug: "cortex", Goal: "scroll the case", Phase: domain.PhaseInvestigating}},
		detail:   detail{loaded: true, view: view}, width: 60, height: 16,
	}
	if out := m.render(); strings.Contains(out, "tail evidence is reachable") || !strings.Contains(out, "PgUp/PgDn") {
		t.Fatalf("initial viewport should hide the tail and expose scroll help:\n%s", out)
	}
	for range 20 {
		next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
		m = next.(model)
	}
	if m.detailOffset == 0 {
		t.Fatal("PageDown did not advance the detail viewport")
	}
	if out := m.render(); !strings.Contains(out, "tail evidence is reachable") {
		t.Fatalf("PageDown did not reach the detail tail:\n%s", out)
	}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if next.(model).detailOffset >= m.detailOffset {
		t.Fatal("PageUp did not move the detail viewport upward")
	}
	up := next.(model)
	next, _ = up.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if next.(model).detailOffset <= up.detailOffset {
		t.Fatal("Ctrl-D did not move the detail viewport downward")
	}
}

func TestSessionRowsExposePhaseWithoutColorAndUseAvailableWidth(t *testing.T) {
	s := kernel.SessionSummary{Slug: "payments-service", Goal: "verify the checkout redirect", Phase: domain.PhaseVerifying}
	for _, selected := range []bool{false, true} {
		row := ansi.Strip(renderSessionRow(s, selected, 48, time.Now()))
		if !strings.Contains(row, "verify") {
			t.Fatalf("selected=%v row hides phase without color: %q", selected, row)
		}
		if !strings.Contains(row, "checkout") {
			t.Fatalf("selected=%v row did not use responsive goal space: %q", selected, row)
		}
		if width := lipgloss.Width(row); width > 48 {
			t.Fatalf("row width = %d, want <= 48: %q", width, row)
		}
	}
}

func TestClipUsesTerminalCellsAndRemovesControls(t *testing.T) {
	if got := clip("界界界", 5); got != "界界…" || ansi.StringWidth(got) != 5 {
		t.Fatalf("wide-character clip = %q (%d cells), want 界界… (5)", got, ansi.StringWidth(got))
	}
	unsafe := "before\x1b]52;c;Y2xpcGJvYXJk\aafter\rnext\tcolumn\nline\x00"
	got := clip(unsafe, 80)
	for _, r := range got {
		if unicode.IsControl(r) {
			t.Fatalf("clip retained terminal control %U in %q", r, got)
		}
	}
	if strings.Contains(got, "52;c") {
		t.Fatalf("clip retained OSC payload: %q", got)
	}
}

type fakeBoardSource struct {
	sessionsFn   func(kernel.SessionFilter) ([]kernel.SessionSummary, error)
	detailFn     func(string, string) (kernel.SessionView, error)
	sessionCalls int
	detailCalls  int
}

func (f *fakeBoardSource) Sessions(filter kernel.SessionFilter) ([]kernel.SessionSummary, error) {
	f.sessionCalls++
	return f.sessionsFn(filter)
}

func (f *fakeBoardSource) Detail(slug, taskID string) (kernel.SessionView, error) {
	f.detailCalls++
	return f.detailFn(slug, taskID)
}

func TestBoardSourceRunsOnlyWhenCommandExecutes(t *testing.T) {
	source := &fakeBoardSource{
		sessionsFn: func(kernel.SessionFilter) ([]kernel.SessionSummary, error) { return nil, nil },
		detailFn:   func(string, string) (kernel.SessionView, error) { return kernel.SessionView{}, nil },
	}
	m := newModelWithSource(allFilter(), source)
	if initial := m.render(); source.sessionCalls != 0 || !strings.Contains(initial, "loading sessions") || strings.Contains(initial, "0 sessions") {
		t.Fatal("constructing the model should be I/O-free and render a loading state")
	}
	_ = m.Init()
	if source.sessionCalls != 0 {
		t.Fatal("Init should return commands without running the source inline")
	}
	m.refreshInFlight = false // isolate a manual request from the initial command
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if cmd == nil || source.sessionCalls != 0 || !next.(model).refreshInFlight {
		t.Fatal("Update should schedule refresh I/O without executing it")
	}
	_ = cmd()
	if source.sessionCalls != 1 {
		t.Fatalf("executing the command made %d source calls, want 1", source.sessionCalls)
	}
}

func TestBoardCoalescesRefreshesAndRejectsOldFilterResult(t *testing.T) {
	active := kernel.SessionSummary{ID: "task_active", Slug: "repo", Goal: "active", Phase: domain.PhaseInvestigating, Active: true}
	source := &fakeBoardSource{
		sessionsFn: func(filter kernel.SessionFilter) ([]kernel.SessionSummary, error) {
			if !filter.ActiveOnly {
				t.Fatal("queued refresh used the superseded filter")
			}
			return []kernel.SessionSummary{active}, nil
		},
		detailFn: func(string, string) (kernel.SessionView, error) {
			return kernel.SessionView{Case: &domain.CaseFile{ID: active.ID, Goal: active.Goal}}, nil
		},
	}
	m := newModelWithSource(allFilter(), source) // request 1 is in flight
	changed, cmd := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m = changed.(model)
	if cmd != nil || !m.refreshQueued || !m.filter.ActiveOnly || m.refreshRequest != 1 {
		t.Fatalf("filter change was not coalesced behind the in-flight read: %+v", m)
	}
	changed, cmd = m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	m = changed.(model)
	if cmd != nil || m.refreshRequest != 1 {
		t.Fatal("repeated refresh should not start another list read")
	}
	old := sessionsLoadedMsg{
		request: 1, filter: allFilter(),
		sessions: []kernel.SessionSummary{{ID: "task_terminal", Active: false}}, at: time.Now(),
	}
	changed, cmd = m.Update(old)
	m = changed.(model)
	if len(m.sessions) != 0 || cmd == nil || !m.refreshInFlight || m.refreshRequest != 2 {
		t.Fatalf("old-filter result applied or queued refresh did not start: %+v", m)
	}
	changed, _ = m.Update(cmd())
	m = changed.(model)
	if len(m.sessions) != 1 || m.sessions[0].ID != active.ID {
		t.Fatalf("new-filter result did not win: %+v", m.sessions)
	}
}

func TestBoardCoalescesSearchBehindInitialRefresh(t *testing.T) {
	match := kernel.SessionSummary{ID: "task_match", Slug: "billing", Goal: "repair redirect"}
	source := &fakeBoardSource{
		sessionsFn: func(filter kernel.SessionFilter) ([]kernel.SessionSummary, error) {
			if filter.Query != "billing redirect" {
				t.Fatalf("queued search used query %q", filter.Query)
			}
			return []kernel.SessionSummary{match}, nil
		},
		detailFn: func(_ string, taskID string) (kernel.SessionView, error) {
			return kernel.SessionView{Case: &domain.CaseFile{ID: taskID}}, nil
		},
	}
	m := newModelWithSource(allFilter(), source) // initial unfiltered request is in flight
	m.searchEditing = true
	m.searchDraft = " billing   redirect "
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if cmd != nil || !m.refreshQueued || m.filter.Query != "billing redirect" {
		t.Fatalf("search was not coalesced behind the initial read: %+v", m)
	}
	updated, cmd = m.Update(sessionsLoadedMsg{
		request: 1, filter: allFilter(),
		sessions: []kernel.SessionSummary{{ID: "task_old"}}, at: time.Now(),
	})
	m = updated.(model)
	if cmd == nil || len(m.sessions) != 0 || m.filterApplied {
		t.Fatalf("old-query result was applied before queued search: %+v", m)
	}
	updated, _ = m.Update(cmd())
	m = updated.(model)
	if !m.filterApplied || m.appliedFilter.Query != "billing redirect" || len(m.sessions) != 1 || m.sessions[0].ID != match.ID {
		t.Fatalf("queued search did not become authoritative: %+v", m)
	}
}

func TestBoardPreservesSelectionAcrossReorder(t *testing.T) {
	a := kernel.SessionSummary{ID: "task_a", Slug: "repo", Goal: "A"}
	b := kernel.SessionSummary{ID: "task_b", Slug: "repo", Goal: "B"}
	source := &fakeBoardSource{
		sessionsFn: func(kernel.SessionFilter) ([]kernel.SessionSummary, error) { return nil, nil },
		detailFn: func(_ string, taskID string) (kernel.SessionView, error) {
			return kernel.SessionView{Case: &domain.CaseFile{ID: taskID, Goal: taskID}}, nil
		},
	}
	m := newModelWithSource(allFilter(), source)
	m.sessions, m.cursor = []kernel.SessionSummary{a, b}, 0
	updated, cmd := m.Update(sessionsLoadedMsg{request: 1, filter: allFilter(), sessions: []kernel.SessionSummary{b, a}, at: time.Now()})
	m = updated.(model)
	if m.cursor != 1 || m.selectedID() != a.ID || cmd == nil {
		t.Fatalf("selection was not preserved by ID across reorder: cursor=%d id=%q", m.cursor, m.selectedID())
	}
}

func TestBoardSelectsFallbackWhenCurrentSessionDisappears(t *testing.T) {
	a := kernel.SessionSummary{ID: "task_a", Slug: "repo", Goal: "A"}
	b := kernel.SessionSummary{ID: "task_b", Slug: "repo", Goal: "B"}
	source := &fakeBoardSource{
		sessionsFn: func(kernel.SessionFilter) ([]kernel.SessionSummary, error) { return nil, nil },
		detailFn: func(_ string, taskID string) (kernel.SessionView, error) {
			return kernel.SessionView{Case: &domain.CaseFile{ID: taskID, Goal: taskID}}, nil
		},
	}
	m := newModelWithSource(allFilter(), source)
	m.sessions, m.cursor = []kernel.SessionSummary{a, b}, 1
	m.detail = detail{loaded: true, view: kernel.SessionView{Case: &domain.CaseFile{ID: b.ID, Goal: "old B"}}}
	m.detailOffset = 8
	updated, cmd := m.Update(sessionsLoadedMsg{request: 1, filter: allFilter(), sessions: []kernel.SessionSummary{a}, at: time.Now()})
	m = updated.(model)
	if m.selectedID() != a.ID || m.detail.loaded || m.detailOffset != 0 || cmd == nil {
		t.Fatalf("removed selection did not reset and schedule the fallback: %+v", m)
	}
}

func TestBoardIgnoresStaleRequestIDs(t *testing.T) {
	m := model{
		filter: allFilter(), refreshRequest: 2, refreshInFlight: true,
		sessions:      []kernel.SessionSummary{{ID: "task_current"}},
		detailRequest: 2, detailInFlight: true, detailTarget: "task_current",
	}
	updated, cmd := m.Update(sessionsLoadedMsg{
		request: 1, filter: allFilter(), sessions: []kernel.SessionSummary{{ID: "task_stale"}}, at: time.Now(),
	})
	m = updated.(model)
	if cmd != nil || m.sessions[0].ID != "task_current" || !m.refreshInFlight {
		t.Fatalf("stale list request mutated current state: %+v", m)
	}
	updated, cmd = m.Update(detailLoadedMsg{
		request: 1, taskID: "task_current",
		view: kernel.SessionView{Case: &domain.CaseFile{ID: "task_current", Goal: "stale detail"}},
	})
	m = updated.(model)
	if cmd != nil || m.detail.loaded || !m.detailInFlight {
		t.Fatalf("stale detail request mutated current state: %+v", m)
	}
}

func TestStaleDetailCannotOverwriteNewSelection(t *testing.T) {
	a := kernel.SessionSummary{ID: "task_a", Slug: "repo", Goal: "A"}
	b := kernel.SessionSummary{ID: "task_b", Slug: "repo", Goal: "B"}
	source := &fakeBoardSource{
		sessionsFn: func(kernel.SessionFilter) ([]kernel.SessionSummary, error) { return nil, nil },
		detailFn: func(_ string, taskID string) (kernel.SessionView, error) {
			return kernel.SessionView{Case: &domain.CaseFile{ID: taskID, Goal: "detail " + taskID}}, nil
		},
	}
	m := newModelWithSource(allFilter(), source)
	m.refreshInFlight = false
	m.sessions = []kernel.SessionSummary{a, b}
	m.detail = detail{loaded: true, view: kernel.SessionView{Case: &domain.CaseFile{ID: a.ID, Goal: "old A"}}}
	m.detailInFlight, m.detailRequest, m.detailTarget = true, 1, a.ID
	m.detailOffset = 9
	selected, cmd := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = selected.(model)
	if cmd != nil || m.selectedID() != b.ID || m.detail.loaded || m.detailOffset != 0 || !m.detailQueued {
		t.Fatalf("selection change did not hide mismatched detail immediately: %+v", m)
	}
	if out := m.renderDetail(80); strings.Contains(out, "old A") || !strings.Contains(out, b.ID) {
		t.Fatalf("new selection rendered mismatched old detail:\n%s", out)
	}
	selected, cmd = m.Update(detailLoadedMsg{
		request: 1, taskID: a.ID,
		view: kernel.SessionView{Case: &domain.CaseFile{ID: a.ID, Goal: "late A"}},
	})
	m = selected.(model)
	if cmd == nil || m.detail.loaded || m.detailTarget != b.ID {
		t.Fatalf("late A result was applied or B was not scheduled: %+v", m)
	}
	selected, _ = m.Update(cmd())
	m = selected.(model)
	if !m.detail.loaded || m.detail.view.Case.ID != b.ID || strings.Contains(m.renderDetail(80), "late A") {
		t.Fatalf("B detail did not become authoritative: %+v", m.detail)
	}
}

func TestBoardErrorsAreIndependentAndLastGoodDetailSurvives(t *testing.T) {
	session := kernel.SessionSummary{ID: "task_a", Slug: "repo", Goal: "A"}
	lastGood := kernel.SessionView{Case: &domain.CaseFile{ID: session.ID, Goal: "last good projection", Status: domain.PhaseInvestigating}}
	source := &fakeBoardSource{
		sessionsFn: func(kernel.SessionFilter) ([]kernel.SessionSummary, error) {
			return []kernel.SessionSummary{session}, nil
		},
		detailFn: func(string, string) (kernel.SessionView, error) {
			return kernel.SessionView{}, errors.New("temporarily busy")
		},
	}
	m := newModelWithSource(allFilter(), source)
	m.sessions = []kernel.SessionSummary{session}
	m.refreshInFlight = false
	m.refreshErr = "refresh failed: list unavailable"
	m.detail = detail{loaded: true, view: lastGood}
	cmd := m.requestDetail()
	updated, _ := m.Update(cmd())
	m = updated.(model)
	if !m.detail.loaded || m.detail.view.Case.ID != session.ID || m.detailErr == "" || m.refreshErr == "" {
		t.Fatalf("detail failure did not retain same-task data and independent errors: %+v", m)
	}
	if out := m.render(); !strings.Contains(out, "refresh failed") || !strings.Contains(out, "session load failed") || !strings.Contains(out, "last good projection") {
		t.Fatalf("combined error state is not visible with last-good detail:\n%s", out)
	}

	// A successful detail refresh clears only the detail error.
	source.detailFn = func(string, string) (kernel.SessionView, error) { return lastGood, nil }
	cmd = m.requestDetail()
	updated, _ = m.Update(cmd())
	m = updated.(model)
	if m.detailErr != "" || m.refreshErr == "" {
		t.Fatalf("detail success cleared the wrong error domain: refresh=%q detail=%q", m.refreshErr, m.detailErr)
	}

	// A successful list refresh clears only the refresh error until detail succeeds.
	m.detailErr = "session load failed: still stale"
	cmd = m.requestRefresh(false)
	updated, _ = m.Update(cmd())
	m = updated.(model)
	if m.refreshErr != "" || m.detailErr == "" {
		t.Fatalf("list success cleared the wrong error domain: refresh=%q detail=%q", m.refreshErr, m.detailErr)
	}
}

func TestDetailRefreshPreservesMidScrollAndFollowsTail(t *testing.T) {
	viewWithHypotheses := func(count int) kernel.SessionView {
		hyps := make([]domain.Hypothesis, 0, count)
		for i := range count {
			hyps = append(hyps, domain.Hypothesis{Statement: fmt.Sprintf("hypothesis-%02d", i)})
		}
		return kernel.SessionView{Case: &domain.CaseFile{ID: "task_scroll", Goal: "scroll"}, Hypotheses: hyps}
	}
	for _, tc := range []struct {
		name     string
		atBottom bool
	}{
		{name: "mid-scroll"},
		{name: "tail", atBottom: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := model{
				sessions: []kernel.SessionSummary{{ID: "task_scroll", Slug: "repo"}},
				detail:   detail{loaded: true, view: viewWithHypotheses(20)}, width: 60, height: 16,
				detailInFlight: true, detailRequest: 1, detailTarget: "task_scroll",
			}
			_, oldMaximum := m.detailScrollBounds()
			m.detailOffset = oldMaximum / 2
			if tc.atBottom {
				m.detailOffset = oldMaximum
			}
			oldOffset := m.detailOffset
			updated, _ := m.Update(detailLoadedMsg{request: 1, taskID: "task_scroll", view: viewWithHypotheses(30)})
			m = updated.(model)
			_, newMaximum := m.detailScrollBounds()
			if tc.atBottom && (m.detailOffset != newMaximum || newMaximum <= oldMaximum) {
				t.Fatalf("tail did not follow appended detail: old=%d new=%d offset=%d", oldMaximum, newMaximum, m.detailOffset)
			}
			if !tc.atBottom && m.detailOffset != oldOffset {
				t.Fatalf("mid-scroll moved from %d to %d", oldOffset, m.detailOffset)
			}
		})
	}
}

func assertTerminalBounds(t *testing.T, out string, width, height int) {
	t.Helper()
	lines := strings.Split(out, "\n")
	if len(lines) > height {
		t.Fatalf("rendered %d lines, want <= %d:\n%s", len(lines), height, out)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d is %d cells, want <= %d: %q", i+1, got, width, ansi.Strip(line))
		}
	}
}
