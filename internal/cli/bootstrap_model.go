package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kuchmenko/workspace/internal/bootstrap"
	"github.com/kuchmenko/workspace/internal/branchprompt"
	"github.com/kuchmenko/workspace/internal/clone"
	"github.com/kuchmenko/workspace/internal/conflict"
)

type bootstrapStep int

const (
	bsStepPlan bootstrapStep = iota
	bsStepCloning
	bsStepBranchPrompt
	bsStepDone
)

type bootstrapError struct {
	project string
	err     error
}

type bootstrapModel struct {
	step          bootstrapStep
	stepChangedAt time.Time
	width         int
	height        int

	plan      *bootstrap.Plan
	toClone   []bootstrap.PlanItem
	current   int
	successes []string
	errors    []bootstrapError
	canceled  bool

	spinner spinner.Model
	sidecar *bootstrap.Sidecar

	branchPrompt branchprompt.Model
	branchAnswer chan branchAnswer
}

type branchAnswer struct {
	branch string
	err    error
}

type cloneDoneMsg struct {
	index   int
	project string
	res     *clone.Result
	err     error
}
type needsBranchMsg struct {
	project    string
	candidates []string
	answer     chan branchAnswer
}

type allDoneMsg struct{}

var program *tea.Program

func newBootstrapModel(plan *bootstrap.Plan, toClone []bootstrap.PlanItem, resume map[string]bootstrap.DoneEntry) bootstrapModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))

	sc := bootstrap.New(wsRoot)
	for k, v := range resume {
		_ = sc.Set(k, v)
	}

	return bootstrapModel{
		step:    bsStepPlan,
		plan:    plan,
		toClone: toClone,
		spinner: sp,
		sidecar: sc,
	}
}

func (m bootstrapModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m bootstrapModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:

		if !m.stepChangedAt.IsZero() && time.Since(m.stepChangedAt) < 100*time.Millisecond {
			return m, nil
		}
		if msg.String() == "ctrl+c" {
			m.canceled = true
			return m, tea.Quit
		}
	}

	switch m.step {
	case bsStepPlan:
		return m.updatePlan(msg)
	case bsStepCloning:
		return m.updateCloning(msg)
	case bsStepBranchPrompt:
		return m.updateBranchPrompt(msg)
	case bsStepDone:
		if _, ok := msg.(tea.KeyMsg); ok {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m bootstrapModel) updatePlan(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "y", "Y", "enter":
			if len(m.toClone) == 0 {
				m.step = bsStepDone
				return m, tea.Quit
			}

			if err := bootstrap.Save(m.sidecar); err != nil {
				m.errors = append(m.errors, bootstrapError{project: "<sidecar>", err: err})
				return m, tea.Quit
			}
			conflict.Notify("ws: bootstrap started",
				fmt.Sprintf("%s: cloning %d projects", wsRoot, len(m.toClone)))
			m.step = bsStepCloning
			m.stepChangedAt = time.Now()
			return m, tea.Batch(m.spinner.Tick, m.startClone(0))
		case "n", "N", "escape":
			m.canceled = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m bootstrapModel) startClone(index int) tea.Cmd {
	if index >= len(m.toClone) {
		return func() tea.Msg { return allDoneMsg{} }
	}
	item := m.toClone[index]
	return func() tea.Msg {
		proj := item.Project

		ch := make(chan branchAnswer, 1)
		opts := clone.Options{
			Logf: func(format string, args ...interface{}) {
			},
			PromptDefaultBranch: func(name string, candidates []string) (string, error) {
				program.Send(needsBranchMsg{
					project:    name,
					candidates: candidates,
					answer:     ch,
				})
				ans := <-ch
				return ans.branch, ans.err
			},
		}
		res, err := clone.CloneIntoLayout(wsRoot, item.Name, &proj, opts)

		return cloneDoneMsg{index: index, project: item.Name, res: res, err: err}
	}
}

func (m bootstrapModel) updateCloning(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case needsBranchMsg:

		m.step = bsStepBranchPrompt
		m.stepChangedAt = time.Now()
		m.branchPrompt = branchprompt.NewModel(msg.project, msg.candidates)
		m.branchAnswer = msg.answer
		return m, m.branchPrompt.Init()

	case cloneDoneMsg:
		if msg.err != nil {
			m.errors = append(m.errors, bootstrapError{project: msg.project, err: msg.err})
		} else {
			m.successes = append(m.successes, msg.project)

			if msg.res != nil {
				_ = m.sidecar.MarkDone(msg.project, msg.res.DefaultBranch)
				_ = bootstrap.Save(m.sidecar)
			}
		}
		m.current = msg.index + 1

		if m.current > 0 && m.current%5 == 0 && m.current < len(m.toClone) {
			conflict.Notify("ws: bootstrap progress",
				fmt.Sprintf("%d/%d cloned", m.current, len(m.toClone)))
		}
		if m.current >= len(m.toClone) {
			m.step = bsStepDone
			return m, tea.Quit
		}
		return m, m.startClone(m.current)

	case allDoneMsg:
		m.step = bsStepDone
		return m, tea.Quit
	}
	return m, nil
}

func (m bootstrapModel) updateBranchPrompt(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case branchprompt.PickedMsg:
		m.resolveBranch(msg.Branch, nil)
		m.step = bsStepCloning
		m.stepChangedAt = time.Now()
		return m, nil
	case branchprompt.CancelledMsg:

		m.resolveBranch("", errors.New("user canceled branch selection"))
		m.step = bsStepCloning
		m.stepChangedAt = time.Now()
		return m, nil
	}

	var cmd tea.Cmd
	m.branchPrompt, cmd = m.branchPrompt.Update(msg)
	return m, cmd
}

func (m *bootstrapModel) resolveBranch(branch string, err error) {
	if m.branchAnswer == nil {
		return
	}
	m.branchAnswer <- branchAnswer{branch: branch, err: err}
	m.branchAnswer = nil
}
