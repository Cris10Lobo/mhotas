package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"mhotas/internal/control"
	"mhotas/internal/transport"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	bannerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true) // sky blue
	subtitleStyle = lipgloss.NewStyle().Faint(true).Italic(true)
	dimStyle      = lipgloss.NewStyle().Faint(true)
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // green
	waitStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // yellow
	paneStyle     = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1)
)

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Model is the dashboard's bubbletea state. It never drives the server or
// backend — Status()/Ready()/Lines() are all read-only snapshots polled on
// a tick, matching how the app's own control_screen.dart treats telemetry
// as something to display, not something driving local logic.
type Model struct {
	srv     *transport.Server
	backend control.Output
	tail    *LogTail

	banner string // rendered once at startup, static for the process lifetime

	connectAddr string
	qr          string
	qrErr       error

	// logs is a bounded, scrollable viewport rather than "however many
	// lines fit" computed from a guessed chrome height — a fixed-size
	// component structurally can't push the header/panes above it out of
	// view the way a misjudged line-count budget did (the header/panes
	// were shorter than their real rendered height once the QR code's own
	// size was accounted for, so more log lines were shown than actually
	// fit, and the terminal scrolled everything up as a result).
	logs      viewport.Model
	logsReady bool // false until the first WindowSizeMsg gives real dimensions
}

func New(srv *transport.Server, backend control.Output, tail *LogTail) Model {
	m := Model{srv: srv, backend: backend, tail: tail}

	// Non-fatal if this fails (a font-data bug, say) — the dashboard's job
	// is showing sim/device status, not the banner; fall back to plain
	// text rather than let a cosmetic failure take down the display.
	if banner, err := renderBanner(); err == nil {
		m.banner = banner
	}

	ip, err := localIPv4()
	if err != nil {
		m.qrErr = err
		return m
	}
	m.connectAddr = fmt.Sprintf("%s:%d", ip, srv.Addr().Port)
	m.qr, m.qrErr = connectQR(m.connectAddr)
	return m
}

func (m Model) Init() tea.Cmd {
	return tick()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Measured, not guessed: an earlier version used a hardcoded
		// constant here that didn't account for the QR code's own height
		// (it scales with the connect string's length), which was the
		// actual root cause of logs pushing the header/panes off-screen —
		// the log budget was computed against a chrome height smaller than
		// chrome actually was. lipgloss.Height measures what really
		// rendered, so this can't drift out of sync with topBlock/
		// headerBlock again.
		//
		// The banner+QR+status block alone is ~30 rows — below that
		// terminal height there's no room for any log pane regardless
		// (see the zero-floor comment below), and eventually not even for
		// the full header on a truly tiny terminal. That's an inherent
		// size floor of the dashboard, not a bug to route around here.
		chromeHeight := lipgloss.Height(m.headerBlock()) + 1 /* blank */ +
			lipgloss.Height(m.topBlock()) + 1 /* blank */ + 1 /* "Recent activity" label */
		// No floor here on purpose: a forced minimum (log pane always at
		// least N lines) is exactly the bug this replaced — on a terminal
		// too short to fit the header/panes plus any logs, an empty log
		// pane is the correct outcome, not a guaranteed overflow.
		logHeight := max(msg.Height-chromeHeight, 0)
		if !m.logsReady {
			m.logs = viewport.New(msg.Width, logHeight)
			m.logsReady = true
		} else {
			m.logs.Width = msg.Width
			m.logs.Height = logHeight
		}
		m.refreshLogs()
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}
		var cmd tea.Cmd
		m.logs, cmd = m.logs.Update(msg) // arrow/j-k/pgup-pgdn/u-d — see viewport.DefaultKeyMap
		return m, cmd
	case tickMsg:
		m.refreshLogs()
		return m, tick()
	}
	return m, nil
}

// refreshLogs pushes the latest tail into the viewport, auto-following new
// lines the way `tail -f` does — but only if the user was already at the
// bottom, so scrolling up to read older lines doesn't get yanked back down
// by the next tick.
func (m *Model) refreshLogs() {
	if !m.logsReady {
		return
	}
	wasAtBottom := m.logs.AtBottom()
	m.logs.SetContent(strings.Join(m.tail.Lines(), "\n"))
	if wasAtBottom {
		m.logs.GotoBottom()
	}
}

func (m Model) headerBlock() string {
	header := bannerStyle.Render(m.banner)
	if m.banner == "" {
		header = titleStyle.Render("mHOTAS Server") // renderBanner failed — plain-text fallback
	}
	subheader := subtitleStyle.Render("Mobile Hands On Throttle And Stick") +
		"   " + dimStyle.Render("(q to quit, ↑/↓ to scroll log)")
	return header + "\n" + subheader
}

func (m Model) topBlock() string {
	var connectPane string
	if m.qrErr != nil {
		connectPane = fmt.Sprintf("QR unavailable: %s\n\nEnter the server's address\nmanually in the app.", m.qrErr)
	} else {
		connectPane = m.qr + "\nScan to connect: " + m.connectAddr
	}

	status := m.srv.Status()
	ready := m.backend.Ready()
	statusPane := strings.Join([]string{
		"Backend:  " + m.backend.Name(),
		"Sim:      " + dot(ready) + " " + simLabel(ready),
		"Phone:    " + dot(status.DeviceConnected) + " " + phoneLabel(status),
	}, "\n")

	return lipgloss.JoinHorizontal(lipgloss.Top,
		paneStyle.Render(connectPane),
		paneStyle.Render(statusPane),
	)
}

func (m Model) View() string {
	if !m.logsReady {
		// First frame, before any WindowSizeMsg has arrived — bubbletea
		// sends one immediately on start, so this is on screen for a
		// single frame at most.
		return m.headerBlock() + "\n\n" + m.topBlock() + "\n"
	}
	return m.headerBlock() + "\n\n" + m.topBlock() +
		"\n\n" + dimStyle.Render("Recent activity") + "\n" + m.logs.View()
}

func dot(ok bool) string {
	if ok {
		return okStyle.Render("●")
	}
	return waitStyle.Render("○")
}

func simLabel(ready bool) string {
	if ready {
		return okStyle.Render("Connected")
	}
	return waitStyle.Render("Waiting for MSFS2024...")
}

func phoneLabel(s transport.Status) string {
	if s.DeviceConnected {
		name := s.DeviceName
		if name == "" {
			name = "(unnamed device)"
		}
		return okStyle.Render(name)
	}
	if s.LastPhoneSeen.IsZero() {
		return waitStyle.Render("Waiting for phone...")
	}
	return waitStyle.Render(fmt.Sprintf("Disconnected (last seen %s ago)",
		time.Since(s.LastPhoneSeen).Round(time.Second)))
}
