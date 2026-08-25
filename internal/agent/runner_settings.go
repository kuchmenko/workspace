package agent

import (
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/tui"
)

func (m *Model) openRunnerPrefix() {
	machine, _ := config.LoadMachineConfig()
	m.runnerPrefix.SetValue(config.RunnerIDPrefix(machine))
	m.runnerPrefix.Focus()
	m.runnerPrefixError = ""
	m.mode = viewRunnerPrefix
}

func (m *Model) updateRunnerPrefix(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.closeRunnerPrefix()
		return m, nil
	case "enter":
		return m.submitRunnerPrefix()
	}
	var cmd tui.Cmd
	m.runnerPrefix, cmd = m.runnerPrefix.Update(msg)
	return m, cmd
}

func (m *Model) submitRunnerPrefix() (tui.Model, tui.Cmd) {
	machine, err := config.LoadMachineConfig()
	if err != nil {
		m.runnerPrefixError = err.Error()
		return m, nil
	}
	machine.RunnerIDPrefix = strings.TrimSpace(m.runnerPrefix.Value())
	if err := config.SaveMachineConfig(machine); err != nil {
		m.runnerPrefixError = err.Error()
		return m, nil
	}
	m.closeRunnerPrefix()
	return m, nil
}

func (m *Model) closeRunnerPrefix() {
	m.runnerPrefix.Blur()
	m.runnerPrefixError = ""
	m.mode = viewRunners
}

func (m *Model) viewRunnerPrefix() string {
	width := min(64, max(30, m.width-4))
	rows := []string{
		whichKeyTitleStyle.Render("Amp runner settings"),
		"",
		"Runner ID prefix",
		flashSearchStyle.Width(width - 4).Render(m.runnerPrefix.View()),
		"",
		dimStyle.Render("Used for new runners. Existing runner IDs stay unchanged."),
	}
	if m.runnerPrefixError != "" {
		rows = append(rows, "", statusMsgStyle.Render(m.runnerPrefixError))
	}
	rows = append(rows, "", dimStyle.Render("Enter save · Esc cancel"))
	panel := whichKeyBorderStyle.Width(width - 2).Render(tui.JoinVertical(tui.Left, rows...))
	return tui.Overlay(tui.DimCanvas(m.width, m.height, m.viewRunners()), panel, m.width, m.height)
}
