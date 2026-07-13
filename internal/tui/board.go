// Package tui is the Cortex studio: a live, read-only Charm v2 (bubbletea) board
// of every session across every repository. It shows a session list and the
// selected case's loop progress, hypotheses, evidence ledger, and verification
// receipts in a responsive split or stacked layout. It reads the central XDG
// sessions tree via internal/kernel and auto-refreshes; it never mutates a case.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
	"github.com/abdul-hamid-achik/cortex/internal/kernel"
)

// refreshInterval is how often the board re-reads the sessions tree.
const refreshInterval = 2 * time.Second

// staleBoardThreshold is how long an in-flight session may sit untouched before
// the board flags it as stale (matches the CLI default).
const staleBoardThreshold = 24 * time.Hour

// splitPaneMinWidth is the smallest terminal width where the list and detail
// panes remain useful side by side. Narrower terminals stack the panes and give
// the detail view most of the available height.
const splitPaneMinWidth = 80

// maxSearchRunes keeps pasted search text cheap to render and bounded inside
// the terminal. The kernel accepts longer non-interactive queries; this limit
// applies only to Studio's interactive editor.
const maxSearchRunes = 128

// Run launches the live board over all sessions matching filter until quit.
func Run(ctx context.Context, filter kernel.SessionFilter) error {
	m := newModel(filter)
	_, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	return err
}

type boardSource interface {
	Sessions(kernel.SessionFilter) ([]kernel.SessionSummary, error)
	Detail(slug, taskID string) (kernel.SessionView, error)
}

type kernelBoardSource struct{}

func (kernelBoardSource) Sessions(filter kernel.SessionFilter) ([]kernel.SessionSummary, error) {
	return kernel.AllSessions(filter)
}

func (kernelBoardSource) Detail(slug, taskID string) (kernel.SessionView, error) {
	return kernel.LoadSessionView(slug, taskID)
}

type model struct {
	filter        kernel.SessionFilter
	appliedFilter kernel.SessionFilter
	filterApplied bool
	source        boardSource
	sessions      []kernel.SessionSummary
	cursor        int
	detail        detail
	width         int
	height        int
	refreshErr    string
	detailErr     string
	lastRefresh   time.Time
	detailOffset  int

	refreshRequest  uint64
	detailRequest   uint64
	refreshInFlight bool
	detailInFlight  bool
	refreshQueued   bool
	detailQueued    bool
	detailTarget    string
	searchEditing   bool
	searchDraft     string
}

// detail is the canonical read-only projection of the selected case. Keeping
// Studio on SessionView prevents its interpretation of verification, decisions,
// and next actions from drifting from `cortex show` and machine surfaces.
type detail struct {
	loaded bool
	view   kernel.SessionView
}

type tickMsg time.Time

type sessionsLoadedMsg struct {
	request  uint64
	filter   kernel.SessionFilter
	sessions []kernel.SessionSummary
	err      error
	at       time.Time
}

type detailLoadedMsg struct {
	request uint64
	taskID  string
	view    kernel.SessionView
	err     error
}

func tick() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func newModel(filter kernel.SessionFilter) model {
	return newModelWithSource(filter, kernelBoardSource{})
}

func newModelWithSource(filter kernel.SessionFilter, source boardSource) model {
	filter.Query = normalizeSearch(filter.Query)
	return model{
		filter: filter, source: source, width: 100, height: 30,
		refreshRequest: 1, refreshInFlight: true,
		searchDraft: filter.Query,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.sessionsCmd(m.refreshRequest, m.filter), tick())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampDetailOffset()
	case tickMsg:
		return m, tea.Batch(m.requestRefresh(false), tick())
	case sessionsLoadedMsg:
		return m.handleSessionsLoaded(msg)
	case detailLoadedMsg:
		return m.handleDetailLoaded(msg)
	case tea.KeyPressMsg:
		if m.searchEditing {
			return m.updateSearch(msg)
		}
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				return m.selectSession(m.cursor - 1)
			}
		case "down", "j":
			if m.cursor < len(m.sessions)-1 {
				return m.selectSession(m.cursor + 1)
			}
		case "g", "home":
			return m.selectSession(0)
		case "G", "end":
			return m.selectSession(max(0, len(m.sessions)-1))
		case "pgup", "ctrl+u":
			m.scrollDetail(-1)
		case "pgdown", "ctrl+d":
			m.scrollDetail(1)
		case "a":
			m.filter.ActiveOnly = !m.filter.ActiveOnly
			return m, m.requestRefresh(true)
		case "/":
			m.searchEditing = true
			m.searchDraft = m.filter.Query
		case "c":
			if m.filter.Query != "" {
				m.filter.Query = ""
				m.searchDraft = ""
				return m, m.requestRefresh(true)
			}
		case "r":
			return m, m.requestRefresh(true)
		}
	}
	return m, nil
}

func (m model) updateSearch(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.searchEditing = false
		m.searchDraft = m.filter.Query
		return m, nil
	case "enter":
		m.searchEditing = false
		query := normalizeSearch(m.searchDraft)
		m.searchDraft = query
		if query == m.filter.Query {
			return m, nil
		}
		m.filter.Query = query
		return m, m.requestRefresh(true)
	case "backspace":
		m.searchDraft = trimLastRune(m.searchDraft)
		return m, nil
	}
	if msg.Text != "" {
		m.searchDraft = appendSearchText(m.searchDraft, msg.Text)
	}
	return m, nil
}

func normalizeSearch(value string) string {
	return strings.Join(strings.Fields(appendSearchText("", value)), " ")
}

func appendSearchText(current, added string) string {
	runes := []rune(current)
	for _, r := range ansi.Strip(added) {
		if len(runes) >= maxSearchRunes {
			break
		}
		if unicode.IsPrint(r) && !unicode.IsControl(r) {
			runes = append(runes, r)
		}
	}
	return string(runes)
}

func quoteSearch(value string) string {
	return `"` + singleLine(value) + `"`
}

func trimLastRune(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return ""
	}
	return string(runes[:len(runes)-1])
}

func (m model) sessionsCmd(request uint64, filter kernel.SessionFilter) tea.Cmd {
	return func() tea.Msg {
		sessions, err := m.source.Sessions(filter)
		return sessionsLoadedMsg{request: request, filter: filter, sessions: sessions, err: err, at: time.Now()}
	}
}

func (m model) detailCmd(request uint64, session kernel.SessionSummary) tea.Cmd {
	return func() tea.Msg {
		view, err := m.source.Detail(session.Slug, session.ID)
		return detailLoadedMsg{request: request, taskID: session.ID, view: view, err: err}
	}
}

func (m *model) requestRefresh(queueIfBusy bool) tea.Cmd {
	if m.refreshInFlight {
		if queueIfBusy {
			m.refreshQueued = true
		}
		return nil
	}
	m.refreshRequest++
	m.refreshInFlight = true
	return m.sessionsCmd(m.refreshRequest, m.filter)
}

func (m *model) requestDetail() tea.Cmd {
	session, ok := m.selectedSession()
	if !ok {
		m.detail = detail{}
		m.detailErr = ""
		m.detailOffset = 0
		m.detailQueued = false
		return nil
	}
	if m.detailInFlight {
		m.detailQueued = m.detailTarget != session.ID
		return nil
	}
	m.detailRequest++
	m.detailInFlight = true
	m.detailTarget = session.ID
	return m.detailCmd(m.detailRequest, session)
}

func (m model) selectedID() string {
	if session, ok := m.selectedSession(); ok {
		return session.ID
	}
	return ""
}

func (m model) selectedSession() (kernel.SessionSummary, bool) {
	if m.cursor < 0 || m.cursor >= len(m.sessions) {
		return kernel.SessionSummary{}, false
	}
	return m.sessions[m.cursor], true
}

func (m *model) clearMismatchedDetail(taskID string) {
	if !m.detail.loaded || m.detail.view.Case == nil || m.detail.view.Case.ID != taskID {
		m.detail = detail{}
	}
}

func (m model) selectSession(cursor int) (tea.Model, tea.Cmd) {
	oldID := m.selectedID()
	if len(m.sessions) == 0 {
		cursor = 0
	} else {
		cursor = clamp(cursor, 0, len(m.sessions)-1)
	}
	m.cursor = cursor
	newID := m.selectedID()
	if newID == oldID {
		return m, nil
	}
	m.detailOffset = 0
	m.detailErr = ""
	m.clearMismatchedDetail(newID)
	return m, m.requestDetail()
}

func (m model) handleSessionsLoaded(msg sessionsLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.request != m.refreshRequest {
		return m, nil
	}
	m.refreshInFlight = false
	var detailCmd tea.Cmd
	if msg.filter == m.filter {
		if msg.err != nil {
			m.refreshErr = "refresh failed: " + msg.err.Error()
		} else {
			selectedID := m.selectedID()
			m.sessions = msg.sessions
			m.cursor = 0
			for i, session := range m.sessions {
				if session.ID == selectedID {
					m.cursor = i
					break
				}
			}
			newID := m.selectedID()
			if newID != selectedID {
				m.detailOffset = 0
				m.detailErr = ""
				m.clearMismatchedDetail(newID)
			}
			m.refreshErr = ""
			m.lastRefresh = msg.at
			m.appliedFilter = msg.filter
			m.filterApplied = true
			detailCmd = m.requestDetail()
		}
	}
	var refreshCmd tea.Cmd
	if m.refreshQueued {
		m.refreshQueued = false
		refreshCmd = m.requestRefresh(false)
	}
	return m, tea.Batch(detailCmd, refreshCmd)
}

func (m model) handleDetailLoaded(msg detailLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.request != m.detailRequest || msg.taskID != m.detailTarget {
		return m, nil
	}
	m.detailInFlight = false
	m.detailTarget = ""
	selectedID := m.selectedID()
	if msg.taskID == selectedID {
		if msg.err != nil {
			m.detailErr = "session load failed: " + msg.err.Error()
			m.clearMismatchedDetail(selectedID)
		} else {
			sameTask := m.detail.loaded && m.detail.view.Case != nil && msg.view.Case != nil && m.detail.view.Case.ID == msg.view.Case.ID
			_, oldMaximum := m.detailScrollBounds()
			atBottom := sameTask && m.detailOffset >= oldMaximum
			m.detail = detail{loaded: true, view: msg.view}
			m.detailErr = ""
			if !sameTask {
				m.detailOffset = 0
			} else if atBottom {
				_, newMaximum := m.detailScrollBounds()
				m.detailOffset = newMaximum
			} else {
				m.clampDetailOffset()
			}
		}
	}
	var cmd tea.Cmd
	if m.detailQueued || msg.taskID != selectedID {
		m.detailQueued = false
		cmd = m.requestDetail()
	}
	return m, cmd
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
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

type boardLayout struct {
	width, height             int
	bodyHeight                int
	listWidth, listHeight     int
	detailWidth, detailHeight int
	narrow                    bool
	showError                 bool
}

func (m model) boardLayout() boardLayout {
	l := boardLayout{width: max(1, m.width), height: max(1, m.height)}
	headerHeight := 1
	helpHeight := 0
	if l.height >= 2 {
		helpHeight = 1
	}
	if m.errorText() != "" && l.height >= 3 {
		l.showError = true
		headerHeight++
	}
	l.bodyHeight = max(0, l.height-headerHeight-helpHeight)
	l.narrow = l.width < splitPaneMinWidth
	if l.narrow {
		l.listWidth, l.detailWidth = l.width, l.width
		if l.bodyHeight < 6 {
			// At extremely short heights the selected case is more useful than
			// two empty border frames. The header still reports the session count.
			l.detailHeight = l.bodyHeight
			return l
		}
		l.listHeight = clamp(l.bodyHeight/3, 3, 7)
		l.detailHeight = l.bodyHeight - l.listHeight
		return l
	}
	l.listWidth = clamp(l.width/3, 28, 42)
	l.detailWidth = l.width - l.listWidth
	l.listHeight, l.detailHeight = l.bodyHeight, l.bodyHeight
	return l
}

func (m model) render() string {
	l := m.boardLayout()
	stale := 0
	for _, s := range m.sessions {
		if s.StaleSince(time.Now(), staleBoardThreshold) {
			stale++
		}
	}
	displayedFilter := m.filter
	if m.filterApplied {
		displayedFilter = m.appliedFilter
	}
	sub := "  —  loading sessions"
	if !m.filterApplied && !m.refreshInFlight && m.refreshErr != "" {
		sub = "  —  sessions unavailable"
	}
	if m.filterApplied {
		if len(m.sessions) == 0 {
			sub = "  —  0 sessions"
		} else {
			sub = fmt.Sprintf("  —  session %d/%d", m.cursor+1, len(m.sessions))
		}
	}
	if m.filterApplied && m.appliedFilter != m.filter {
		sub += " · applying filters"
	}
	sub += " · auto " + refreshInterval.String()
	if displayedFilter.ActiveOnly {
		sub += " · active"
	}
	if displayedFilter.Repo != "" {
		sub += " · repo~" + singleLine(displayedFilter.Repo)
	}
	if displayedFilter.Query != "" {
		sub += " · search " + quoteSearch(displayedFilter.Query)
	}
	if m.refreshInFlight {
		sub += " · refreshing"
	}
	if m.detailInFlight {
		sub += " · loading detail"
	}
	title := tHeader.Render("● Cortex studio") + tDim.Render(sub)
	if stale > 0 {
		title += tWarn.Render(fmt.Sprintf("  ⚠ %d stale", stale))
	}
	title = truncateStyled(title, l.width)
	helpText := "j/k sessions · PgUp/PgDn detail · / search · a active · r refresh · q quit"
	if m.filter.Query != "" {
		helpText = "j/k sessions · PgUp/PgDn detail · / search · c clear · a active · r refresh · q quit"
	}
	if l.narrow {
		helpText = "j/k sessions · / search · r refresh · q quit · PgUp/PgDn"
		if m.filter.Query != "" {
			helpText = "j/k sessions · / edit · c clear · r refresh · q quit"
		}
	}
	if m.searchEditing {
		helpText = "search · Enter apply · Esc cancel: " + singleLine(m.searchDraft) + "█"
	}
	help := tDim.Render(clip(helpText, l.width))

	parts := []string{title}
	if l.showError {
		errText := m.errorText()
		if m.filterApplied && m.appliedFilter != m.filter {
			errText = "requested filters not applied · " + errText
		}
		if !m.lastRefresh.IsZero() {
			errText += " · showing snapshot from " + m.lastRefresh.Local().Format("15:04:05")
		}
		parts = append(parts, tErr.Render(clip("⚠ "+errText, l.width)))
	}

	if l.bodyHeight > 0 {
		var body string
		if l.narrow {
			panes := make([]string, 0, 2)
			if l.listHeight > 0 {
				listContentW := max(1, l.listWidth-tListBox.GetHorizontalFrameSize())
				listContentH := max(0, l.listHeight-tListBox.GetVerticalFrameSize())
				panes = append(panes, tListBox.Width(l.listWidth).Height(l.listHeight).Render(m.renderList(listContentH, listContentW)))
			}
			if l.detailHeight > 0 {
				detailContentW := max(1, l.detailWidth-tDetailBox.GetHorizontalFrameSize())
				detailContentH := max(0, l.detailHeight-tDetailBox.GetVerticalFrameSize())
				panes = append(panes, tDetailBox.Width(l.detailWidth).Height(l.detailHeight).Render(m.renderDetailViewport(detailContentW, detailContentH)))
			}
			body = lipgloss.JoinVertical(lipgloss.Left, panes...)
		} else {
			listContentW := max(1, l.listWidth-tListBox.GetHorizontalFrameSize())
			listContentH := max(0, l.listHeight-tListBox.GetVerticalFrameSize())
			detailContentW := max(1, l.detailWidth-tDetailBox.GetHorizontalFrameSize())
			detailContentH := max(0, l.detailHeight-tDetailBox.GetVerticalFrameSize())
			list := tListBox.Width(l.listWidth).Height(l.listHeight).Render(m.renderList(listContentH, listContentW))
			det := tDetailBox.Width(l.detailWidth).Height(l.detailHeight).Render(m.renderDetailViewport(detailContentW, detailContentH))
			body = lipgloss.JoinHorizontal(lipgloss.Top, list, det)
		}
		parts = append(parts, body)
	}
	if l.height >= 2 {
		parts = append(parts, help)
	}

	return fitBlock(lipgloss.JoinVertical(lipgloss.Left, parts...), l.width, l.height)
}

func (m model) renderList(h, w int) string {
	if h <= 0 {
		return ""
	}
	if len(m.sessions) == 0 {
		lines := []string{"no sessions yet", "", "open one with:", "cortex open \"<goal>\""}
		if !m.filterApplied && m.refreshInFlight {
			lines = []string{"loading sessions…"}
		} else if !m.filterApplied && m.refreshErr != "" {
			lines = []string{"sessions unavailable", "", "press r to retry"}
		} else if m.appliedFilter.Query != "" {
			lines = []string{"no sessions match " + quoteSearch(m.appliedFilter.Query), "press / edit · c clear"}
		} else if m.appliedFilter.ActiveOnly {
			lines = []string{"no active sessions", "", "press a to show all"}
		} else if m.appliedFilter.Repo != "" {
			lines = []string{"no sessions for repo", singleLine(m.appliedFilter.Repo)}
		}
		if len(lines) > h {
			lines = lines[:h]
		}
		for i := range lines {
			lines[i] = clip(lines[i], w)
		}
		return tDim.Render(strings.Join(lines, "\n"))
	}
	// Window the list around the cursor so long lists scroll into view.
	now := time.Now()
	start := 0
	if m.cursor >= h {
		start = m.cursor - h + 1
	}
	var b strings.Builder
	for i := start; i < len(m.sessions) && i < start+h; i++ {
		b.WriteString(renderSessionRow(m.sessions[i], i == m.cursor, w, now))
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func renderSessionRow(s kernel.SessionSummary, selected bool, w int, now time.Time) string {
	if w <= 0 {
		return ""
	}
	marker := "● "
	if selected {
		marker = "▸ "
	}
	phase := phaseLabel(s.Phase)
	phaseField := fmt.Sprintf("%-7s ", phase)
	suffix := ""
	if s.StaleSince(now, staleBoardThreshold) {
		suffix = " ⚠"
	}
	prefixWidth := ansi.StringWidth(marker + phaseField)
	suffixWidth := ansi.StringWidth(suffix)
	remaining := max(0, w-prefixWidth-suffixWidth)
	descriptor := ""
	if remaining > 0 {
		slugWidth := 0
		if remaining >= 12 {
			slugWidth = min(12, max(6, remaining/3))
		}
		if slugWidth > 0 {
			slug := clip(s.Slug, slugWidth)
			goalWidth := max(0, remaining-ansi.StringWidth(slug)-1)
			descriptor = slug
			if goalWidth > 0 {
				descriptor += " " + clip(s.Goal, goalWidth)
			}
		} else {
			descriptor = clip(s.Goal, remaining)
		}
	}
	plain := clip(marker+phaseField+descriptor+suffix, w)
	if selected {
		return tSel.Render(plain)
	}
	prefix := phaseColor(s.Phase).Render(marker + phaseField)
	rest := descriptor
	if suffix != "" {
		rest += tWarn.Render(suffix)
	}
	return truncateStyled(prefix+rest, w)
}

func (m model) renderDetail(w int) string {
	selectedID := m.selectedID()
	matching := m.detail.loaded && m.detail.view.Case != nil && m.detail.view.Case.ID == selectedID
	if !matching {
		if selectedID == "" {
			if m.refreshInFlight {
				return tDim.Render("waiting for sessions…")
			}
			return tDim.Render("select a session")
		}
		if m.detailInFlight || m.detailQueued {
			return tDim.Render(clip("loading session "+selectedID+"…", w))
		}
		if m.detailErr != "" {
			return tErr.Render(clip(m.detailErr, w))
		}
		return tDim.Render("select a session")
	}
	v := m.detail.view
	c := v.Case
	if c == nil {
		return tErr.Render("session projection has no case")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", tHeader.Render(clip(c.Goal, w-2)))
	phaseStyle := tPhase
	if c.Status == domain.PhaseNeedsHumanDecision {
		phaseStyle = tWarn
	}
	phaseText := "[" + clip(string(c.Status), max(1, w-2)) + "]"
	fmt.Fprintf(&b, "%s  %s\n", tDim.Render(clip(c.ID, max(1, w-18))), phaseStyle.Render(phaseText))
	meta := fmt.Sprintf("repo %s@%s · %s · risk %s", c.Workspace.Repository, c.Workspace.CommitBefore, c.Mode, c.Risk)
	fmt.Fprintf(&b, "%s\n", tDim.Render(clip(meta, w)))
	// The reasoning loop, with "you are here".
	fmt.Fprintf(&b, "%s\n", loopStepper(c.Status))
	if c.Status == domain.PhaseNeedsHumanDecision {
		paused := "⚠ paused for human input · an answer resumes " + string(c.PausedFrom)
		fmt.Fprintf(&b, "%s\n", tWarn.Render(clip(paused, w)))
	}
	b.WriteString("\n")

	receiptTotal := max(v.ReceiptTotal, len(v.Receipts))
	verificationHeading := tSection.Render("Verification") + "  " + assessmentMark(v.VerificationAssessment)
	if receiptTotal > 0 {
		verificationHeading += "  " + tDim.Render(fmt.Sprintf("(%d receipts)", receiptTotal))
	}
	b.WriteString(verificationHeading + "\n")
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
		older := max(0, receiptTotal-(len(v.Receipts)-start))
		if older > 0 {
			fmt.Fprintf(&b, "  %s\n", tDim.Render(fmt.Sprintf("… %d older receipts", older)))
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
			optionText := fmt.Sprintf("[%s] %s — %s", option.ID, option.Label, option.Consequence)
			fmt.Fprintf(&b, "  %s\n", tPhase.Render(clip(optionText, w-2)))
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
			fmt.Fprintf(&b, "    %s\n", tDim.Render(clip("needs: "+strings.Join(action.Inputs, ", "), w-4)))
		}
		b.WriteString("\n")
	}
	for _, warning := range v.ProjectionWarnings {
		fmt.Fprintf(&b, "  %s %s\n", tDim.Render("…"), tDim.Render(clip(warning, w-6)))
	}
	if len(v.ProjectionWarnings) > 0 {
		b.WriteString("\n")
	}

	if len(v.Hypotheses) > 0 {
		b.WriteString(tSection.Render("Hypotheses") + "\n")
		for _, h := range v.Hypotheses {
			fmt.Fprintf(&b, "  %s %s\n", hypMark(h.Status), clip(h.Statement, w-6))
		}
		b.WriteString("\n")
	}

	evidenceTotal := max(v.EvidenceTotal, len(v.Evidence))
	fmt.Fprintf(&b, "%s %s\n", tSection.Render("Recent Evidence"), tDim.Render(fmt.Sprintf("(%d total)", evidenceTotal)))
	start := max(0, len(v.Evidence)-5)
	older := max(0, evidenceTotal-(len(v.Evidence)-start))
	if older > 0 {
		fmt.Fprintf(&b, "  %s\n", tDim.Render(fmt.Sprintf("… %d older", older)))
	}
	for _, e := range v.Evidence[start:] {
		fmt.Fprintf(&b, "  %s %s\n", confMark(e.Confidence), clip(e.Claim, w-8))
	}
	return fitBlockWidth(strings.TrimSuffix(b.String(), "\n"), w)
}

func (m model) errorText() string {
	var errors []string
	if m.refreshErr != "" {
		errors = append(errors, m.refreshErr)
	}
	if m.detailErr != "" {
		errors = append(errors, m.detailErr)
	}
	return strings.Join(errors, " · ")
}

func (m model) detailDimensions() (width, height int) {
	l := m.boardLayout()
	return max(1, l.detailWidth-tDetailBox.GetHorizontalFrameSize()), max(0, l.detailHeight-tDetailBox.GetVerticalFrameSize())
}

func (m model) detailScrollBounds() (page, maximum int) {
	w, h := m.detailDimensions()
	if h <= 0 {
		return 0, 0
	}
	lines := strings.Split(m.renderDetail(w), "\n")
	page = h
	if len(lines) > h {
		page = max(1, h-1) // reserve the last row for the scroll position
	}
	return page, max(0, len(lines)-page)
}

func (m *model) scrollDetail(direction int) {
	page, maximum := m.detailScrollBounds()
	if page <= 0 || maximum == 0 {
		m.detailOffset = 0
		return
	}
	step := max(1, page-1)
	m.detailOffset = clamp(m.detailOffset+direction*step, 0, maximum)
}

func (m *model) clampDetailOffset() {
	_, maximum := m.detailScrollBounds()
	m.detailOffset = clamp(m.detailOffset, 0, maximum)
}

func (m model) renderDetailViewport(w, h int) string {
	if h <= 0 {
		return ""
	}
	lines := strings.Split(m.renderDetail(w), "\n")
	if len(lines) <= h {
		return strings.Join(lines, "\n")
	}
	page := max(1, h-1)
	maximum := max(0, len(lines)-page)
	offset := clamp(m.detailOffset, 0, maximum)
	end := min(len(lines), offset+page)
	visible := append([]string(nil), lines[offset:end]...)
	if h == 1 {
		return strings.Join(visible, "\n")
	}
	position := fmt.Sprintf("lines %d–%d of %d · PgUp/PgDn", offset+1, end, len(lines))
	visible = append(visible, tDim.Render(clip(position, w)))
	return strings.Join(visible, "\n")
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
		track += "  " + tErr.Render("■ "+singleLine(string(p)))
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

func phaseLabel(p domain.Phase) string {
	switch p {
	case domain.PhaseNew, domain.PhaseOrienting:
		return "orient"
	case domain.PhaseInvestigating:
		return "inv"
	case domain.PhasePlanned:
		return "plan"
	case domain.PhaseChanging:
		return "change"
	case domain.PhaseVerifying:
		return "verify"
	case domain.PhasePersisting:
		return "keep"
	case domain.PhaseComplete:
		return "done"
	case domain.PhaseNeedsHumanDecision:
		return "paused"
	case domain.PhaseBlocked:
		return "blocked"
	case domain.PhaseAbandoned:
		return "abandon"
	default:
		return "unknown"
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
	if n < 1 {
		n = 1
	}
	return ansi.Truncate(singleLine(s), n, "…")
}

// singleLine treats every case-file string as untrusted terminal text. ANSI
// and OSC sequences are removed before other controls are flattened so a goal,
// decision, or legacy ledger entry cannot manipulate the operator's terminal.
func singleLine(s string) string {
	s = ansi.Strip(s)
	return strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return ' '
		default:
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}
	}, s)
}

func truncateStyled(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "…")
}

func fitBlockWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = truncateStyled(lines[i], width)
	}
	return strings.Join(lines, "\n")
}

func fitBlock(s string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = truncateStyled(lines[i], width)
	}
	return strings.Join(lines, "\n")
}
