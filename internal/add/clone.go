package add

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/branchprompt"
	"github.com/kuchmenko/workspace/internal/clone"
)

func (m AddModel) startCloneJob(idx int) tea.Cmd {
	if idx >= len(m.queue) {
		return func() tea.Msg { return allClonesDoneMsg{} }
	}
	job := m.queue[idx]
	return func() tea.Msg {
		opts := Options{
			URLs:      []string{job.URL},
			Name:      job.Name,
			Category:  job.Category,
			Group:     job.Group,
			WsRoot:    m.wsRoot,
			Workspace: m.ws,
			Save:      m.saveFn,
			Mode:      ModeHeadless,
			NoClone:   job.FromDisk != "",
		}

		regRes, err := Register(opts, job.URL)
		out := cloneDoneMsg{idx: idx}
		if err != nil {
			if errors.Is(err, ErrAlreadyRegistered) {
				out.skipped = &SkipReason{URL: job.URL, Reason: err.Error()}
			} else if errors.Is(err, clone.ErrNeedsBootstrap) {
				out.err = fmt.Errorf("%s: default branch ambiguous (run `ws bootstrap %s` after add)", job.Name, job.Name)
			} else {
				out.err = err
			}
		} else if regRes != nil {
			out.project = regRes.Project
		}
		return out
	}
}

func (m AddModel) updateCloning(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case cloneDoneMsg:
		switch {
		case msg.err != nil:
			m.errors = append(m.errors, msg.err)
		case msg.skipped != nil:
			m.skipped = append(m.skipped, *msg.skipped)
		default:
			m.added = append(m.added, msg.project)
		}
		m.currentIdx = msg.idx + 1
		if m.currentIdx >= len(m.queue) {
			m.transitionTo(addStateDone)
			if m.standalone {
				return m, tea.Sequence(emit(m.doneMsg()), tea.Quit)
			}
			return m, emit(m.doneMsg())
		}
		return m, m.startCloneJob(m.currentIdx)
	case needsBranchMsg:

		m.branchPrompt = branchprompt.NewModel(msg.project, msg.candidates)
		m.branchAnswer = msg.answer
		m.transitionTo(addStateBranchPrompt)
		return m, nil
	case allClonesDoneMsg:
		m.transitionTo(addStateDone)
		if m.standalone {
			return m, tea.Sequence(emit(m.doneMsg()), tea.Quit)
		}
		return m, emit(m.doneMsg())
	}
	return m, nil
}

func (m AddModel) viewCloning() string {
	var b strings.Builder
	b.WriteString(addTitle.Render(" Cloning "))
	b.WriteString("\n\n")
	total := len(m.queue)
	done := m.currentIdx
	fmt.Fprintf(&b, "  %d / %d\n\n", done, total)
	if m.currentIdx < total {
		j := m.queue[m.currentIdx]
		fmt.Fprintf(&b, "  %s %s\n", m.spinner.View(), j.Name)
		fmt.Fprintf(&b, "    %s\n", addDim.Render(j.Path))
	}
	if len(m.errors) > 0 {
		fmt.Fprintf(&b, "\n  %s %d failed\n", addErr.Render("✗"), len(m.errors))
	}
	b.WriteString("\n  " + addHelp.Render("[ctrl+c] abort"))
	return b.String()
}

func (m AddModel) updateBranchPrompt(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case branchprompt.PickedMsg:
		m.resolveBranch(msg.Branch, nil)
		m.transitionTo(addStateCloning)
		return m, nil
	case branchprompt.CancelledMsg:
		m.resolveBranch("", errors.New("user canceled branch selection"))
		m.transitionTo(addStateCloning)
		return m, nil
	}
	var cmd tea.Cmd
	m.branchPrompt, cmd = m.branchPrompt.Update(msg)
	return m, cmd
}

func (m *AddModel) resolveBranch(branch string, err error) {
	if m.branchAnswer != nil {
		m.branchAnswer <- branchAnswer{branch: branch, err: err}
		m.branchAnswer = nil
	}
}

func (m AddModel) updateDone(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		if m.standalone {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m AddModel) viewDone() string {
	var b strings.Builder
	b.WriteString(addTitle.Render(" Done "))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "  %s %d added\n", addCheck.Render("✓"), len(m.added))
	if len(m.skipped) > 0 {
		fmt.Fprintf(&b, "  %s %d skipped\n", addDim.Render("⊘"), len(m.skipped))
	}
	if len(m.errors) > 0 {
		fmt.Fprintf(&b, "  %s %d errored\n", addErr.Render("✗"), len(m.errors))
		b.WriteString("\n")
		for _, e := range m.errors {
			fmt.Fprintf(&b, "    %s\n", addDim.Render(e.Error()))
		}
	}
	b.WriteString("\n  " + addHelp.Render("[any key] exit"))
	return b.String()
}
