package tui

import (
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

var inputOptions = []struct{ value, label string }{
	{"vjoy", "vJoy        all input through the vJoy device, SimConnect for telemetry"},
	{"simconnect", "SimConnect  all input through SimConnect events, no vJoy"},
}

// ErrCancelled is returned by PickInput when the user quits instead of
// choosing.
var ErrCancelled = errors.New("cancelled")

type picker struct {
	cursor    int
	chosen    bool
	cancelled bool
}

func (p picker) Init() tea.Cmd { return nil }

func (p picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	switch key.String() {
	case "up", "k":
		p.cursor = max(p.cursor-1, 0)
	case "down", "j":
		p.cursor = min(p.cursor+1, len(inputOptions)-1)
	case "enter":
		p.chosen = true
		return p, tea.Quit
	case "q", "ctrl+c", "esc":
		p.cancelled = true
		return p, tea.Quit
	}
	return p, nil
}

func (p picker) View() string {
	if p.chosen || p.cancelled {
		return ""
	}
	s := titleStyle.Render("Control input mode") + "  " + dimStyle.Render("(↑/↓, Enter)") + "\n\n"
	for i, o := range inputOptions {
		cursor := "  "
		if i == p.cursor {
			cursor = okStyle.Render("> ")
		}
		s += cursor + o.label + "\n"
	}
	return s
}

// PickInput asks for the input mode before the server starts; vJoy is
// preselected, so Enter alone keeps the default.
func PickInput() (string, error) {
	m, err := tea.NewProgram(picker{}).Run()
	if err != nil {
		return "", fmt.Errorf("input picker: %w", err)
	}
	p := m.(picker)
	if p.cancelled {
		return "", ErrCancelled
	}
	return inputOptions[p.cursor].value, nil
}
