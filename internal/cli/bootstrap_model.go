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
	current   int // index into toClone
	successes []string
	errors    []bootstrapError
	canceled  bool

	spinner spinner.Model
	sidecar *bootstrap.Sidecar

	// Branch-prompt sub-state. The UI is owned by internal/branchprompt;
	// branchAnswer is how we unblock the worker goroutine waiting on the
	// channel passed into clone.Options.PromptDefaultBranch.
	branchPrompt branchprompt.Model
	branchAnswer chan branchAnswer
}

type branchAnswer struct {
	branch string
	err    error
}

// Custom messages for the async clone loop.
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

// program is the running tea.Program. We need a global handle to it so the
// PromptDefaultBranch callback (running in a worker goroutine) can post
// messages back into the TUI loop. Set in runBootstrap before p.Run().
var program *tea.Program

func newBootstrapModel(plan *bootstrap.Plan, toClone []bootstrap.PlanItem, resume map[string]bootstrap.DoneEntry) bootstrapModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))

	// Initialize sidecar (in-memory only — written to disk after first
	// successful clone, so a Ctrl+C on the plan screen leaves no trace).
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
		// Debounce immediately after step transitions to avoid phantom inputs.
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
			// Persist sidecar with our pid before any clone runs.
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

// startClone returns a tea.Cmd that runs CloneIntoLayout for toClone[index]
// in a goroutine and emits cloneDoneMsg when finished. Branch prompts during
// the clone are routed back through needsBranchMsg → updateBranchPrompt and
// resolved via a channel.
func (m bootstrapModel) startClone(index int) tea.Cmd {
	if index >= len(m.toClone) {
		return func() tea.Msg { return allDoneMsg{} }
	}
	item := m.toClone[index]
	return func() tea.Msg {
		proj := item.Project
		// PromptDefaultBranch bridges into the TUI: send a needsBranchMsg
		// from inside the goroutine using p.Send via the global program?
		// We don't have that handle here, so use a channel-based approach:
		// the prompt callback parks on a channel, the TUI replies via the
		// same channel after the user picks a branch.
		ch := make(chan branchAnswer, 1)
		opts := clone.Options{
			Logf: func(format string, args ...interface{}) {
				// no-op; TUI shows progress, full log goes to debug if needed
			},
			PromptDefaultBranch: func(name string, candidates []string) (string, error) {
				// Send a request into the bubbletea queue and block until
				// the model writes back into ch.
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
		// proj is local to this goroutine; the resolved default_branch is
		// returned via res for the main loop to record into the sidecar.
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
		// Pause clone progress and switch to the branch-prompt sub-step.
		// The UI (candidate list, free-text input, styling) is owned by
		// internal/branchprompt; we keep only the answer channel that
		// unblocks the clone goroutine.
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
			// Persist progress immediately so a crash doesn't lose work.
			if msg.res != nil {
				_ = m.sidecar.MarkDone(msg.project, msg.res.DefaultBranch)
				_ = bootstrap.Save(m.sidecar)
			}
		}
		m.current = msg.index + 1
		// Periodic notify-send progress (every 5 clones).
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
	// Terminal messages from the branchprompt model take priority: a pick
	// or cancel ends the sub-step and unblocks the clone worker.
	switch msg := msg.(type) {
	case branchprompt.PickedMsg:
		m.resolveBranch(msg.Branch, nil)
		m.step = bsStepCloning
		m.stepChangedAt = time.Now()
		return m, nil
	case branchprompt.CancelledMsg:
		// User refuses to pick → treat as error for this project.
		m.resolveBranch("", errors.New("user canceled branch selection"))
		m.step = bsStepCloning
		m.stepChangedAt = time.Now()
		return m, nil
	}

	// Otherwise delegate to the embedded model and let it produce the
	// terminal messages above on the next key event.
	var cmd tea.Cmd
	m.branchPrompt, cmd = m.branchPrompt.Update(msg)
	return m, cmd
}

// resolveBranch unblocks the worker goroutine waiting for a branch answer.
func (m *bootstrapModel) resolveBranch(branch string, err error) {
	if m.branchAnswer == nil {
		return
	}
	m.branchAnswer <- branchAnswer{branch: branch, err: err}
	m.branchAnswer = nil
}
