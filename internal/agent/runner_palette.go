package agent

import "github.com/kuchmenko/workspace/internal/config"

func (m *Model) runnerCommands(section string, target config.RunnerConfig, path string) []paletteCommand {
	info, found := m.targetRunnerInfo(target, path)
	open := command(section, "Open Amp runners", "runner manager", "R", "open-runners")
	if !found || info.Definition.ID == "" {
		if found {
			return []paletteCommand{open}
		}
		create := command(section, "Start Amp runner", "create runner", "r", "runner-create")
		create.runnerTarget = target
		return []paletteCommand{create, open}
	}
	if info.Status == "unmanaged" {
		return []paletteCommand{open}
	}
	var action paletteCommand
	if info.Status == "running" {
		action = command(section, "Restart Amp runner", "runner lifecycle", "r", "runner-restart")
		shutdown := command(section, "Shut down Amp runner", "stop runner", "", "runner-shutdown")
		shutdown.runnerTarget = info.Definition
		action.runnerTarget = info.Definition
		return []paletteCommand{action, shutdown, open}
	}
	action = command(section, "Start Amp runner", "runner lifecycle", "r", "runner-start")
	action.runnerTarget = info.Definition
	return []paletteCommand{action, open}
}

func (m *Model) runnerPaletteCommands() []paletteCommand {
	commands := []paletteCommand{command("AMP RUNNERS", "Set runner ID prefix", "settings prefix", "p", "runner-prefix"), command("AMP RUNNERS", "Return Home", "close", "", "runner-return")}
	info := m.selectedRunner()
	if info == nil {
		return commands
	}
	if info.Status == "unmanaged" {
		replace := command("SELECTED EXTERNAL RUNNER", "Replace with managed runner", "migrate restart", "enter", "runner-replace")
		forceReplace := command("SELECTED EXTERNAL RUNNER", "Force replace with managed runner", "sigkill migrate", "X", "runner-force-replace")
		return append([]paletteCommand{replace, forceReplace}, commands...)
	}
	if info.Definition.ID == "" {
		return commands
	}
	if info.Status == "running" {
		restart := command("SELECTED RUNNER", "Restart", "stop start", "r", "runner-restart")
		shutdown := command("SELECTED RUNNER", "Shut down", "stop", "x", "runner-shutdown")
		forceRestart := command("SELECTED RUNNER", "Force restart", "sigkill stop start", "", "runner-force-restart")
		forceShutdown := command("SELECTED RUNNER", "Force shutdown", "sigkill stop", "X", "runner-force-shutdown")
		restart.runnerTarget, shutdown.runnerTarget = info.Definition, info.Definition
		forceRestart.runnerTarget, forceShutdown.runnerTarget = info.Definition, info.Definition
		commands = append([]paletteCommand{restart, shutdown, forceRestart, forceShutdown}, commands...)
	} else {
		start := command("SELECTED RUNNER", "Start", "launch", "s", "runner-start")
		start.runnerTarget = info.Definition
		commands = append([]paletteCommand{start}, commands...)
	}
	forget := command("SELECTED RUNNER", "Forget", "remove definition", "d", "runner-forget")
	forget.runnerTarget = info.Definition
	return append(commands, forget)
}
