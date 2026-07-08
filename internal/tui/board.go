// Package tui is the Cortex board: a read-only Charm v2 (bubbletea) surface for
// browsing case files — the task list on the left, the selected case's phase,
// hypotheses, evidence ledger, and verification receipts on the right. It is a
// thin viewer over internal/kernel's store; it never mutates a case.
package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
)

// Run launches the board over the given kernel until the user quits.
func Run(ctx context.Context, k *kernel.Kernel) error {
	m, err := newModel(k)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m).Run()
	return err
}

type model struct {
	k       *kernel.Kernel
	tasks   []kernel.TaskSummary
	cursor  int
	detail  detail
	width   int
	height  int
	loadErr string
}

// detail is the loaded view of the selected case (read straight from the store,
// so navigation stays instant — no subprocess health checks).
type detail struct {
	loaded   bool
	c        *domain.CaseFile
	evidence []domain.Evidence
	hyps     []domain.Hypothesis
	receipts []domain.VerificationRecord
}

func newModel(k *kernel.Kernel) (model, error) {
	tasks, err := k.ListTasks()
	if err != nil {
		return model{}, err
	}
	m := model{k: k, tasks: tasks, width: 100, height: 30}
	m.load()
	return m, nil
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
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
			if m.cursor < len(m.tasks)-1 {
				m.cursor++
				m.load()
			}
		case "g", "home":
			m.cursor = 0
			m.load()
		case "G", "end":
			m.cursor = max(0, len(m.tasks)-1)
			m.load()
		case "r":
			if tasks, err := m.k.ListTasks(); err == nil {
				m.tasks = tasks
				if m.cursor >= len(m.tasks) {
					m.cursor = max(0, len(m.tasks)-1)
				}
				m.load()
			}
		}
	}
	return m, nil
}

// load reads the selected case's records from the store.
func (m *model) load() {
	m.detail = detail{}
	if len(m.tasks) == 0 || m.cursor >= len(m.tasks) {
		return
	}
	id := m.tasks[m.cursor].ID
	st := m.k.Store()
	c, err := st.Load(id)
	if err != nil {
		m.loadErr = err.Error()
		return
	}
	ev, _ := st.Evidence(id)
	hyps, _ := st.Hypotheses(id)
	recs, _ := st.Verifications(id)
	m.detail = detail{loaded: true, c: c, evidence: ev, hyps: hyps, receipts: recs}
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
	title := tHeader.Render("● Cortex studio") + tDim.Render("  —  case files")
	help := tDim.Render("↑/↓ navigate · r refresh · q quit")

	listW := 34
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
	if len(m.tasks) == 0 {
		return tDim.Render("no tasks yet\n\nstart one with:\ncortex start \"<goal>\"")
	}
	var b strings.Builder
	for i, t := range m.tasks {
		if i >= h {
			break
		}
		line := fmt.Sprintf("%-13s %s", t.Phase, clip(t.Goal, 16))
		if i == m.cursor {
			b.WriteString(tSel.Render("▸ " + line))
		} else {
			b.WriteString("  " + phaseColor(t.Phase).Render(string(t.Phase)) + " " + clip(t.Goal, 16))
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
		return tDim.Render("select a task")
	}
	c := m.detail.c
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", tHeader.Render(clip(c.Goal, w-2)))
	fmt.Fprintf(&b, "%s  %s\n", tDim.Render(c.ID), tPhase.Render("["+string(c.Status)+"]"))
	fmt.Fprintf(&b, "%s %s@%s · %s · risk %s\n\n",
		tDim.Render("repo"), c.Workspace.Repository, c.Workspace.CommitBefore, c.Mode, c.Risk)

	if len(m.detail.hyps) > 0 {
		b.WriteString(tSection.Render("Hypotheses") + "\n")
		for _, h := range m.detail.hyps {
			fmt.Fprintf(&b, "  %s %s\n", hypMark(h.Status), clip(h.Statement, w-6))
		}
		b.WriteString("\n")
	}

	if len(m.detail.receipts) > 0 {
		b.WriteString(tSection.Render("Verification") + "\n")
		for _, r := range m.detail.receipts {
			fmt.Fprintf(&b, "  %s %s %s\n", receiptMark(r.Status), tDim.Render(string(r.Surface)), clip(r.Claim, w-14))
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "%s %s\n", tSection.Render("Evidence"), tDim.Render(fmt.Sprintf("(%d)", len(m.detail.evidence))))
	shown := 0
	for _, e := range m.detail.evidence {
		if shown >= 8 {
			fmt.Fprintf(&b, "  %s\n", tDim.Render(fmt.Sprintf("… %d more", len(m.detail.evidence)-shown)))
			break
		}
		fmt.Fprintf(&b, "  %s %s\n", confMark(e.Confidence), clip(e.Claim, w-8))
		shown++
	}
	return b.String()
}

func phaseColor(p domain.Phase) lipgloss.Style {
	switch p {
	case domain.PhaseComplete:
		return tOK
	case domain.PhaseBlocked, domain.PhaseAbandoned, domain.PhaseNeedsHumanDecision:
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
