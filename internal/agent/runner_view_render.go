package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/runner"
	"github.com/kuchmenko/workspace/internal/tui"
)

func (m *Model) viewRunners() string {
	width := max(1, m.width)
	machine, _ := config.LoadMachineConfig()
	headerRight := fmt.Sprintf("%d/%d · prefix %s", min(m.runnerCursor+1, len(m.runnerInfos)), len(m.runnerInfos), config.RunnerIDPrefix(machine))
	rows := []string{headerStyle.Width(width).Render(padPanelRight(" Amp runners", headerRight+" ", width))}
	bodyRows := max(1, m.height-2)
	start, end := tui.WindowAround(m.runnerCursor, len(m.runnerInfos), max(1, bodyRows/2))
	columns := runnerListColumns(m.runnerInfos)
	for i := start; i < end; i++ {
		info := m.runnerInfos[i]
		primary, path := runnerRowLines(info, m.runnerOperationStatus(info), columns, width)
		selected := i == m.runnerCursor
		rows = append(rows, renderRunnerRowLine(primary, selected, true, width))
		rows = append(rows, renderRunnerRowLine(path, selected, false, width))
	}
	for len(rows) < m.height-1 {
		rows = append(rows, strings.Repeat(" ", width))
	}
	footer := m.runnerFooter()
	rows = append(rows, footerStyle.Width(width).Render(tui.Truncate(footer, width)))
	return tui.GradientCanvas(m.width, m.height, tui.JoinVertical(tui.Left, rows...))
}

type runnerColumns struct {
	name   int
	status int
	pid    int
}

func runnerListColumns(infos []runner.Info) runnerColumns {
	columns := runnerColumns{name: len("external"), status: len("restarting"), pid: len("PID")}
	for _, info := range infos {
		columns.name = max(columns.name, len(runnerDisplayName(info)))
		if info.PID > 0 {
			columns.pid = max(columns.pid, len(strconv.Itoa(info.PID)))
		}
	}
	columns.name = min(30, columns.name)
	return columns
}

func runnerRowLines(info runner.Info, status string, columns runnerColumns, width int) (string, string) {
	pid := ""
	if info.PID > 0 {
		pid = strconv.Itoa(info.PID)
	}
	name := tui.Truncate(runnerDisplayName(info), columns.name)
	left := fmt.Sprintf("  %-*s", columns.name, name)
	right := fmt.Sprintf("%-*s  %*s", columns.status, status, columns.pid, pid)
	primary := padPanelRight(left, right, width)
	path := "    " + compactRunnerPath(info.Path)
	return tui.Truncate(primary, width), tui.Truncate(path, width)
}

func runnerDisplayName(info runner.Info) string {
	if info.Definition.ID == "" {
		return "external"
	}
	return info.Definition.ID
}

func compactRunnerPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return presentLabel(path)
	}
	relative, err := filepath.Rel(home, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return presentLabel(path)
	}
	return presentLabel(relative)
}

func renderRunnerRowLine(line string, selected, accent bool, width int) string {
	line = tui.Truncate(line, width)
	if !selected {
		return itemStyle.Width(width).Render(line)
	}
	if accent {
		content := ""
		if len(line) > 0 {
			content = line[1:]
		}
		return accentBarStyle.Render("▌") + selectedStyle.Width(width-1).Render(tui.Truncate(content, width-1))
	}
	return selectedStyle.Width(width).Render(line)
}

func (m *Model) runnerFooter() string {
	info := m.selectedRunner()
	if info == nil {
		return " p prefix · q back"
	}
	if info.Status == runner.StatusOccupied {
		return " Enter replace external · X force replace · p prefix · q back"
	}
	if info.Status == runner.StatusRunning {
		return " r restart · x shutdown · X force shutdown · p prefix · q back"
	}
	return " s start · e edit ID · d remove · p prefix · q back"
}

func (m *Model) viewRunnerForm() string {
	form := m.runnerForm
	if form == nil {
		return ""
	}
	width := min(72, max(30, m.width-4))
	target := runnerTargetLabel(form.target)
	remote := "off"
	if form.remote {
		remote = "on"
	}
	title := "Create Amp runner"
	if form.originalID != "" {
		title = "Edit Amp runner"
	}
	rows := []string{
		whichKeyTitleStyle.Render(title),
		"",
		"Runner ID",
		flashSearchStyle.Width(width - 4).Render(m.runnerID.View()),
	}
	rows = append(rows, "", "Target", dimStyle.Render(target))
	if form.originalID == "" {
		rows = append(rows, "", "Remote terminal  "+remote)
	}
	if form.error != "" {
		rows = append(rows, "", statusMsgStyle.Render(form.error))
	}
	hint := "Enter create and start · Esc cancel"
	if form.originalID != "" {
		hint = "Enter save · Esc cancel"
	} else {
		hint = "Tab move · Space toggle · " + hint
	}
	rows = append(rows, "", dimStyle.Render(hint))
	panel := whichKeyBorderStyle.Width(width - 2).Render(tui.JoinVertical(tui.Left, rows...))
	background := m.viewList()
	if m.runnerReturnMode == viewRunners {
		background = m.viewRunners()
	} else if m.runnerReturnSheet != nil {
		background = m.runnerReturnSheet.view(m)
	}
	return tui.Overlay(tui.DimCanvas(m.width, m.height, background), panel, m.width, m.height)
}

func (m *Model) viewRunnerConfirm() string {
	confirm := m.runnerConfirm
	if confirm == nil {
		return m.viewRunners()
	}
	width := min(68, max(30, m.width-4))
	action := confirm.action
	if action == "forget" {
		action = "remove"
	}
	title := strings.ToUpper(action[:1]) + action[1:]
	name := confirm.definition.ID
	if confirm.action == "replace" {
		name = confirm.external.Path
	}
	rows := []string{
		whichKeyTitleStyle.Render(title + " Amp runner"),
		"",
		name,
		"",
		"Amp does not expose whether this runner is busy.",
		"Active work may be interrupted.",
		"",
		dimStyle.Render("y/Enter confirm · n/Esc cancel"),
	}
	panel := whichKeyBorderStyle.Width(width - 2).Render(tui.JoinVertical(tui.Left, rows...))
	return tui.Overlay(tui.DimCanvas(m.width, m.height, m.viewRunners()), panel, m.width, m.height)
}
