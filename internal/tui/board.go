// Package tui is the Cortex studio: a live, read-only Charm v2 (bubbletea) board
// of every session across every repository — the session list on the left, and
// the selected case's loop progress, hypotheses, evidence ledger, and
// verification receipts on the right. It reads the central XDG sessions tree via
// internal/kernel and auto-refreshes; it never mutates a case.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
)

// refreshInterval is how often the board re-reads the sessions tree.
const refreshInterval = 2 * time.Second

// staleBoardThreshold is how long an in-flight session may sit untouched before
// the board flags it as stale (matches the CLI default).
const staleBoardThreshold = 24 * time.Hour

// Run launches the live board over all sessions matching filter until quit.
func Run(ctx context.Context, filter kernel.SessionFilter) error {
	m, err := newModel(filter)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m).Run()
	return err
}

type model struct {
	filter      kernel.SessionFilter
	sessions    []kernel.SessionSummary
	cursor      int
	detail      detail
	width       int
	height      int
	loadErr     string
	lastRefresh time.Time
}

// detail is the canonical read-only projection of the selected case. Keeping
// Studio on SessionView prevents its interpretation of verification, decisions,
// and next actions from drifting from `cortex show` and machine surfaces.
type detail struct {
	loaded bool
	view   kernel.SessionView
}

// tickMsg drives auto-refresh.
type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func newModel(filter kernel.SessionFilter) (model, error) {
	m := model{filter: filter, width: 100, height: 30}
	sessions, err := kernel.AllSessions(filter)
	if err != nil {
		return model{}, err
	}
	m.sessions = sessions
	m.lastRefresh = time.Now()
	m.load()
	return m, nil
}

func (m model) Init() tea.Cmd { return tick() }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		m.refresh()
		return m, tick()
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				m.load()
			}
		case "down", "j":
			if m.cursor < len(m.sessions)-1 {
				m.cursor++
				m.load()
			}
		case "g", "home":
			m.cursor = 0
			m.load()
		case "G", "end":
			m.cursor = max(0, len(m.sessions)-1)
			m.load()
		case "a":
			m.filter.ActiveOnly = !m.filter.ActiveOnly
			m.refresh()
		case "r":
			m.refresh()
		}
	}
	return m, nil
}

// refresh re-reads the sessions tree, preserving the selected session by ID.
func (m *model) refresh() {
	selID := ""
	if m.cursor < len(m.sessions) {
		selID = m.sessions[m.cursor].ID
	}
	sessions, err := kernel.AllSessions(m.filter)
	if err != nil {
		m.loadErr = err.Error()
		return
	}
	m.sessions = sessions
	m.cursor = 0
	for i, s := range sessions {
		if s.ID == selID {
			m.cursor = i
			break
		}
	}
	if m.cursor >= len(m.sessions) {
		m.cursor = max(0, len(m.sessions)-1)
	}
	m.lastRefresh = time.Now()
	m.load()
}

// load reads the selected session's records from its store.
func (m *model) load() {
	m.detail = detail{}
	if len(m.sessions) == 0 || m.cursor >= len(m.sessions) {
		return
	}
	s := m.sessions[m.cursor]
	d, err := kernel.LoadSessionView(s.Slug, s.ID)
	if err != nil {
		m.loadErr = err.Error()
		return
	}
	m.detail = detail{loaded: true, view: d}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// styles (Charm v2 lipgloss).
var (
	tHeader    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
	tDim       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	tSel       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("14"))
	tPhase     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	tOK        = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	tWarn      = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	tErr       = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	tSection   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	tListBox   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8")).Padding(0, 1)
	tDetailBox = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8")).Padding(0, 1)
)

// View wraps the rendered board in a v2 tea.View with the alternate screen on.
func (m model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m model) render() string {
	stale := 0
	for _, s := range m.sessions {
		if s.StaleSince(time.Now(), staleBoardThreshold) {
			stale++
		}
	}
	sub := fmt.Sprintf("  —  %d sessions · auto %s", len(m.sessions), refreshInterval)
	if m.filter.ActiveOnly {
		sub += " · active"
	}
	if m.filter.Repo != "" {
		sub += " · repo~" + m.filter.Repo
	}
	title := tHeader.Render("● Cortex studio") + tDim.Render(sub)
	if stale > 0 {
		title += tWarn.Render(fmt.Sprintf("  ⚠ %d stale", stale))
	}
	help := tDim.Render("↑/↓ navigate · a active-only · r refresh · q quit")

	listW := 32
	detailW := m.width - listW - 6
	if detailW < 30 {
		detailW = 30
	}
	bodyH := m.height - 4
	if bodyH < 6 {
		bodyH = 6
	}

	list := tListBox.Width(listW).Height(bodyH).Render(m.renderList(bodyH))
	det := tDetailBox.Width(detailW).Height(bodyH).Render(m.renderDetail(detailW))
	body := lipgloss.JoinHorizontal(lipgloss.Top, list, det)

	return lipgloss.JoinVertical(lipgloss.Left, title, body, help)
}

func (m model) renderList(h int) string {
	if len(m.sessions) == 0 {
		return tDim.Render("no sessions yet\n\nstart one with:\ncortex start \"<goal>\"")
	}
	// Window the list around the cursor so long lists scroll into view.
	now := time.Now()
	start := 0
	if m.cursor >= h {
		start = m.cursor - h + 1
	}
	var b strings.Builder
	for i := start; i < len(m.sessions) && i < start+h; i++ {
		s := m.sessions[i]
		suffix := ""
		if s.StaleSince(now, staleBoardThreshold) {
			suffix = " " + tWarn.Render("⚠") // in-flight but untouched — likely forgotten
		}
		// Both prefixes are two cells wide ("▸ " and "● ") so the slug/goal columns
		// stay aligned as the cursor moves between rows.
		if i == m.cursor {
			b.WriteString(tSel.Render("▸ "+clip(s.Slug, 10)+" "+clip(s.Goal, 13)) + suffix)
		} else {
			b.WriteString(phaseColor(s.Phase).Render("●") + " " +
				clip(s.Slug, 10) + " " + tDim.Render(clip(s.Goal, 13)) + suffix)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (m model) renderDetail(w int) string {
	if !m.detail.loaded {
		if m.loadErr != "" {
			return tErr.Render("load error: " + m.loadErr)
		}
		return tDim.Render("select a session")
	}
	v := m.detail.view
	c := v.Case
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", tHeader.Render(clip(c.Goal, w-2)))
	phaseStyle := tPhase
	if c.Status == domain.PhaseNeedsHumanDecision {
		phaseStyle = tWarn
	}
	fmt.Fprintf(&b, "%s  %s\n", tDim.Render(c.ID), phaseStyle.Render("["+string(c.Status)+"]"))
	fmt.Fprintf(&b, "%s %s@%s · %s · risk %s\n", tDim.Render("repo"), c.Workspace.Repository, c.Workspace.CommitBefore, c.Mode, c.Risk)
	// The reasoning loop, with "you are here".
	fmt.Fprintf(&b, "%s\n", loopStepper(c.Status))
	if c.Status == domain.PhaseNeedsHumanDecision {
		fmt.Fprintf(&b, "%s\n", tWarn.Render("⚠ paused for human input · an answer resumes "+string(c.PausedFrom)))
	}
	b.WriteString("\n")

	b.WriteString(tSection.Render("Verification") + "  " + assessmentMark(v.VerificationAssessment) + "\n")
	for _, gap := range assessmentGaps(v.VerificationAssessment) {
		fmt.Fprintf(&b, "  %s %s\n", tWarn.Render("⚠"), clip(gap, w-6))
	}
	for _, warning := range v.VerificationWarnings {
		fmt.Fprintf(&b, "  %s %s\n", tWarn.Render("⚠"), clip(warning, w-6))
	}
	if len(v.StaleVerification) > 0 {
		fmt.Fprintf(&b, "  %s %s\n", tWarn.Render("⚠"), clip(fmt.Sprintf("%d receipt(s) stale for current HEAD/diff", len(v.StaleVerification)), w-6))
	}
	if len(v.Receipts) > 0 {
		start := max(0, len(v.Receipts)-4)
		if start > 0 {
			fmt.Fprintf(&b, "  %s\n", tDim.Render(fmt.Sprintf("… %d older receipts", start)))
		}
		for _, r := range v.Receipts[start:] {
			mark, suffix := receiptMark(r.Status), ""
			staleKey := r.ID
			if staleKey == "" {
				staleKey = r.Claim
			}
			if containsExact(v.StaleVerification, staleKey) {
				mark, suffix = tWarn.Render("!"), tWarn.Render(" (stale)")
			}
			fmt.Fprintf(&b, "  %s %s %s%s\n", mark, tDim.Render(string(r.Surface)), clip(r.Claim, w-14), suffix)
		}
	}
	b.WriteString("\n")

	if decision := pendingDecision(v.Decisions); decision != nil {
		b.WriteString(tSection.Render("Decision needed") + "\n")
		fmt.Fprintf(&b, "  %s %s\n", tWarn.Render("?"), clip(decision.Question, w-6))
		for _, option := range decision.Options {
			fmt.Fprintf(&b, "  %s %s — %s\n", tPhase.Render("["+option.ID+"]"), clip(option.Label, 24), clip(option.Consequence, w-36))
		}
		b.WriteString("\n")
	}

	if len(v.Actions) > 0 {
		action := v.Actions[0]
		b.WriteString(tSection.Render("Next") + "\n")
		fmt.Fprintf(&b, "  %s %s\n", tPhase.Render("→"), clip(actionLabel(action), w-6))
		if action.Reason != "" {
			fmt.Fprintf(&b, "    %s\n", tDim.Render(clip(action.Reason, w-6)))
		}
		if len(action.Inputs) > 0 {
			fmt.Fprintf(&b, "    %s\n", tDim.Render("needs: "+strings.Join(action.Inputs, ", ")))
		}
		b.WriteString("\n")
	}

	if len(v.Hypotheses) > 0 {
		b.WriteString(tSection.Render("Hypotheses") + "\n")
		for _, h := range v.Hypotheses {
			fmt.Fprintf(&b, "  %s %s\n", hypMark(h.Status), clip(h.Statement, w-6))
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "%s %s\n", tSection.Render("Recent Evidence"), tDim.Render(fmt.Sprintf("(%d total)", len(v.Evidence))))
	start := max(0, len(v.Evidence)-5)
	if start > 0 {
		fmt.Fprintf(&b, "  %s\n", tDim.Render(fmt.Sprintf("… %d older", start)))
	}
	for _, e := range v.Evidence[start:] {
		fmt.Fprintf(&b, "  %s %s\n", confMark(e.Confidence), clip(e.Claim, w-8))
	}
	return b.String()
}

func containsExact(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func pendingDecision(decisions []domain.Decision) *domain.Decision {
	for i := len(decisions) - 1; i >= 0; i-- {
		if decisions[i].Status == domain.DecisionPending {
			return &decisions[i]
		}
	}
	return nil
}

func assessmentMark(a kernel.VerificationAssessment) string {
	switch a.Outcome {
	case kernel.VerificationVerified:
		return tOK.Render("✓ verified")
	case kernel.VerificationFailed:
		return tErr.Render("✗ failed")
	case kernel.VerificationPartial:
		return tWarn.Render("~ partial")
	default:
		return tDim.Render("○ unverified")
	}
}

func assessmentGaps(a kernel.VerificationAssessment) []string {
	gaps := make([]string, 0, len(a.MissingRequired)+len(a.NonPassingClaims)+len(a.FailedClaims))
	for _, requirement := range a.MissingRequired {
		gaps = append(gaps, "missing verifier: "+requirement)
	}
	for _, claim := range a.NonPassingClaims {
		gaps = append(gaps, "not passing: "+claim)
	}
	for _, claim := range a.FailedClaims {
		gaps = append(gaps, "failed claim: "+claim)
	}
	return gaps
}

func actionLabel(action domain.NextAction) string {
	if action.Command != "" {
		return action.Command
	}
	return action.Tool
}

// loopStepper draws orient─inv─plan─change─verify─keep (domain.LoopStages) with
// the current step highlighted, completed steps green, and a stop marker for
// terminal-bad phases.
func loopStepper(p domain.Phase) string {
	sep := tDim.Render("─")
	if p == domain.PhaseComplete {
		parts := make([]string, len(domain.LoopStages))
		for i, s := range domain.LoopStages {
			parts[i] = tOK.Render(s.Label)
		}
		return strings.Join(parts, sep) + " " + tOK.Render("✓")
	}
	cur := domain.LoopStageIndexOf(p)
	parts := make([]string, len(domain.LoopStages))
	for i, s := range domain.LoopStages {
		switch {
		case cur >= 0 && i < cur:
			parts[i] = tOK.Render(s.Label)
		case i == cur:
			parts[i] = tPhase.Render("[" + s.Label + "]")
		default:
			parts[i] = tDim.Render(s.Label)
		}
	}
	track := strings.Join(parts, sep)
	if p == domain.PhaseNeedsHumanDecision {
		track += "  " + tWarn.Render("⏸ paused · needs human decision")
	} else if cur < 0 { // blocked / abandoned
		track += "  " + tErr.Render("■ "+string(p))
	}
	return track
}

func phaseColor(p domain.Phase) lipgloss.Style {
	switch p {
	case domain.PhaseComplete:
		return tOK
	case domain.PhaseNeedsHumanDecision:
		return tWarn
	case domain.PhaseBlocked, domain.PhaseAbandoned:
		return tErr
	default:
		return tPhase
	}
}

func hypMark(s domain.HypothesisStatus) string {
	switch s {
	case domain.HypConfirmed:
		return tOK.Render("✓")
	case domain.HypRejected:
		return tErr.Render("✗")
	case domain.HypChallenged:
		return tWarn.Render("?")
	default:
		return tDim.Render("•")
	}
}

func receiptMark(s domain.VerificationStatus) string {
	switch s {
	case domain.VerifyPassed:
		return tOK.Render("✓")
	case domain.VerifyFailed:
		return tErr.Render("✗")
	case domain.VerifyInconclusive, domain.VerifyBlocked:
		return tWarn.Render("~")
	default:
		return tDim.Render("○")
	}
}

func confMark(c domain.Confidence) string {
	switch c {
	case domain.ConfidenceHigh:
		return tOK.Render("●")
	case domain.ConfidenceMedium:
		return tWarn.Render("●")
	default:
		return tDim.Render("○")
	}
}

func clip(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if n < 1 {
		n = 1
	}
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
