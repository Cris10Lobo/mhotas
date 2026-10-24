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

// Model is the dashboard's bubbletea state; it only polls snapshots.
type Model struct {
	srv     *transport.Server
	backend control.Output
	tail    *LogTail

	banner string
	input  string

	connectAddr string
	altAddrs    []string
	qr          string
	qrErr       error

	// A fixed-size viewport keeps the log pane from pushing the header and
	// panes off-screen, as a guessed line budget once did.
	logs      viewport.Model
	logsReady bool
}

func New(srv *transport.Server, backend control.Output, tail *LogTail) Model {
	m := Model{srv: srv, backend: backend, tail: tail, input: inputLabel(backend)}

	// A banner failure is cosmetic; headerBlock falls back to plain text.
	if banner, err := renderBanner(); err == nil {
		m.banner = banner
	}

	ip, alts, err := localIPv4()
	if err != nil {
		m.qrErr = err
		return m
	}
	m.connectAddr = fmt.Sprintf("%s:%d", ip, srv.Addr().Port)
	for _, alt := range alts {
		m.altAddrs = append(m.altAddrs, fmt.Sprintf("%s:%d", alt, srv.Addr().Port))
	}
	m.qr, m.qrErr = connectQR(m.connectAddr)
	return m
}

func (m Model) Init() tea.Cmd {
	return tick()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Measure the real chrome height: it depends on the QR code size.
		chromeHeight := lipgloss.Height(m.headerBlock()) + 1 /* blank */ +
			lipgloss.Height(m.topBlock()) + 1 /* blank */ + 1 /* "Recent activity" label */
		// No minimum: on a short terminal an empty log pane beats overflow.
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
		m.logs, cmd = m.logs.Update(msg)
		return m, cmd
	case tickMsg:
		m.refreshLogs()
		return m, tick()
	}
	return m, nil
}

// refreshLogs follows new lines only if the user is already at the bottom.
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
		header = titleStyle.Render("mHOTAS Server")
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
		if len(m.altAddrs) > 0 {
			connectPane += "\nStuck on Connecting? Try: " + strings.Join(m.altAddrs, ", ")
		}
	}

	status := m.srv.Status()
	ready := m.backend.Ready()
	textBlock := "Input:    " + m.input + "\n" +
		"Sim:      " + simLabel(ready) + "\n" +
		"Phone:    " + phoneLabel(status)

	qrPane := paneStyle.Render(connectPane)
	// Size the status box to the QR box: border plus padding is 4 wide, 2 tall.
	innerWidth := lipgloss.Width(qrPane) - 4
	innerHeight := lipgloss.Height(qrPane) - 2

	diagram := lipgloss.NewStyle().Width(innerWidth).Align(lipgloss.Center).
		Render(statusDiagram(ready, status.DeviceConnected))

	// Diagram centered above the text readout, which is pinned to the bottom.
	remaining := max(innerHeight-lipgloss.Height(textBlock), 0)
	topFiller := max((remaining-lipgloss.Height(diagram))/2, 0)
	bottomFiller := max(remaining-lipgloss.Height(diagram)-topFiller, 0)
	statusContent := strings.Repeat("\n", topFiller) + diagram +
		strings.Repeat("\n", bottomFiller+1) + textBlock

	return lipgloss.JoinHorizontal(lipgloss.Top,
		qrPane,
		paneStyle.Width(innerWidth).Render(statusContent),
	)
}

func (m Model) View() string {
	if !m.logsReady {
		return m.headerBlock() + "\n\n" + m.topBlock() + "\n"
	}
	return m.headerBlock() + "\n\n" + m.topBlock() +
		"\n\n" + dimStyle.Render("Recent activity") + "\n" + m.logs.View()
}

// statusDiagram shows the sim and phone links converging on a bus that is
// online only when both are live.
func statusDiagram(simReady, phoneOnline bool) string {
	bothOnline := simReady && phoneOnline
	busLabel := waitStyle.Render("OFFLINE")
	if bothOnline {
		busLabel = okStyle.Render("ONLINE")
	}
	rows := []string{
		lipgloss.PlaceHorizontal(9, lipgloss.Center, "SIM") + "   " +
			lipgloss.PlaceHorizontal(9, lipgloss.Center, "PHONE"),
		lipgloss.PlaceHorizontal(9, lipgloss.Center, dot(simReady)) + "   " +
			lipgloss.PlaceHorizontal(9, lipgloss.Center, dot(phoneOnline)),
		"    │           │",
		"    └─────┬─────┘",
		"          │",
		"        " + dot(bothOnline) + " " + busLabel,
	}
	// Pad rows to equal width so the pane's per-line centering shifts them together.
	width := 0
	for _, r := range rows {
		if w := lipgloss.Width(r); w > width {
			width = w
		}
	}
	for i, r := range rows {
		rows[i] = lipgloss.PlaceHorizontal(width, lipgloss.Left, r)
	}
	return strings.Join(rows, "\n")
}

func dot(ok bool) string {
	if ok {
		return okStyle.Render("●")
	}
	return waitStyle.Render("○")
}

func inputLabel(backend control.Output) string {
	switch backend.Name() {
	case "vjoy":
		return "vJoy"
	case "simconnect":
		return "SimConnect"
	}
	return backend.Name()
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
