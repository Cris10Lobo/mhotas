package tui

import (
	"fmt"
	"strings"
	"time"

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

	height int
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
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}
		return m, nil
	case tickMsg:
		return m, tick()
	}
	return m, nil
}

func (m Model) View() string {
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

	top := lipgloss.JoinHorizontal(lipgloss.Top,
		paneStyle.Render(connectPane),
		paneStyle.Render(statusPane),
	)

	logPane := m.logView()

	header := bannerStyle.Render(m.banner)
	if m.banner == "" {
		header = titleStyle.Render("mHOTAS Server") // renderBanner failed — plain-text fallback
	}
	subheader := subtitleStyle.Render("Mobile Hands On Throttle And Stick") +
		"   " + dimStyle.Render("(q to quit)")

	return header + "\n" + subheader +
		"\n\n" + top + "\n\n" + dimStyle.Render("Recent activity") + "\n" + logPane + "\n"
}

// logView shows the tail end of recent log lines, sized to whatever
// terminal height is left after the header/panes above — a fixed count
// would either waste space on a tall terminal or overflow a short one.
func (m Model) logView() string {
	// The larry3d banner is ~9 lines tall on its own; folded into one
	// approximate constant along with the panes/labels below it rather
	// than measuring every element's real height live.
	const chromeLines = 21
	maxLines := max(m.height-chromeLines, 3)
	lines := m.tail.Lines()
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return dimStyle.Render(strings.Join(lines, "\n"))
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
