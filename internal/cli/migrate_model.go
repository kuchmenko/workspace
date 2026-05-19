package cli

import (
	"fmt"
	"time"

	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/migrate"
	"github.com/kuchmenko/workspace/internal/tui"
)

type migrateStep int

const (
	mStepPlan migrateStep = iota
	mStepDecision
	mStepMigrating
	mStepDone
)

type migrateError struct {
	project string
	err     error
}

type migrateModel struct {
	step          migrateStep
	stepChangedAt time.Time

	machine string
	plan    *migratePlan
	queue   []migratePlanItem
	cursor  int
	current migratePlanItem

	decisions map[string]migrateDecision

	successes []string
	errors    []migrateError
	skipped   int
	canceled  bool

	spinner tui.Spinner
	sidecar *migrate.Sidecar
}

type migrateDecision struct {
	WIP             bool
	StashBranch     bool
	CheckoutDefault bool
	Skip            bool
}

type migrateDoneMsg struct {
	index   int
	project string
	res     *migrate.Result
	err     error
}

type migrateAllDoneMsg struct{}

func newMigrateModel(plan *migratePlan, machine string, resume map[string]migrate.DoneEntry) migrateModel {
	sp := tui.NewSpinner()
	sp.SetStyle(tui.DotSpinner)
	sp.SetTextStyle(tui.NewStyle().Foreground("6"))

	sc := migrate.New(wsRoot)
	for k, v := range resume {
		_ = sc.Set(k, v)
	}

	return migrateModel{
		step:      mStepPlan,
		machine:   machine,
		plan:      plan,
		decisions: make(map[string]migrateDecision),
		spinner:   sp,
		sidecar:   sc,
	}
}

func (m migrateModel) Init() tui.Cmd {
	return m.spinner.Tick
}

func (m migrateModel) Update(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch msg := msg.(type) {
	case tui.KeyMsg:
		if !m.stepChangedAt.IsZero() && time.Since(m.stepChangedAt) < 100*time.Millisecond {
			return m, nil
		}
		if msg.String() == "ctrl+c" {
			m.canceled = true
			return m, tui.Quit
		}
	}

	switch m.step {
	case mStepPlan:
		return m.updatePlan(msg)
	case mStepDecision:
		return m.updateDecision(msg)
	case mStepMigrating:
		return m.updateMigrating(msg)
	case mStepDone:
		if _, ok := msg.(tui.KeyMsg); ok {
			return m, tui.Quit
		}
	}
	return m, nil
}

func (m migrateModel) updatePlan(msg tui.Msg) (tui.Model, tui.Cmd) {
	if key, ok := msg.(tui.KeyMsg); ok {
		switch key.String() {
		case "y", "Y", "enter":

			for _, s := range []migrateState{mstReady, mstDirty, mstStash, mstDetached} {
				m.queue = append(m.queue, m.plan.Bucket(s)...)
			}
			if len(m.queue) == 0 {
				m.step = mStepDone
				return m, tui.Quit
			}

			if err := migrate.Save(m.sidecar); err != nil {
				m.errors = append(m.errors, migrateError{project: "<sidecar>", err: err})
				return m, tui.Quit
			}
			conflict.Notify("ws: migrate started",
				fmt.Sprintf("%s: %d projects", wsRoot, len(m.queue)))
			return m.advance()
		case "n", "N", "escape":
			m.canceled = true
			return m, tui.Quit
		}
	}
	return m, nil
}

func (m migrateModel) advance() (tui.Model, tui.Cmd) {
	if m.cursor >= len(m.queue) {
		m.step = mStepDone
		return m, tui.Quit
	}
	m.current = m.queue[m.cursor]
	switch m.current.State {
	case mstReady:

		m.step = mStepMigrating
		m.stepChangedAt = time.Now()
		return m, tui.Batch(m.spinner.Tick, m.startMigrate(m.cursor))
	case mstDirty, mstStash, mstDetached:
		m.step = mStepDecision
		m.stepChangedAt = time.Now()
		return m, nil
	}

	m.skipped++
	m.cursor++
	return m.advance()
}

func (m migrateModel) updateDecision(msg tui.Msg) (tui.Model, tui.Cmd) {
	key, ok := msg.(tui.KeyMsg)
	if !ok {
		return m, nil
	}
	dec := migrateDecision{}
	resolved := false
	switch m.current.State {
	case mstDirty:
		switch key.String() {
		case "w", "W":
			dec.WIP = true
			resolved = true
		case "s", "S":
			dec.Skip = true
			resolved = true
		case "a", "A":
			m.canceled = true
			return m, tui.Quit
		}
	case mstStash:
		switch key.String() {
		case "b", "B":
			dec.StashBranch = true
			resolved = true
		case "s", "S":
			dec.Skip = true
			resolved = true
		case "a", "A":
			m.canceled = true
			return m, tui.Quit
		}
	case mstDetached:
		switch key.String() {
		case "c", "C":
			dec.CheckoutDefault = true
			resolved = true
		case "s", "S":
			dec.Skip = true
			resolved = true
		case "a", "A":
			m.canceled = true
			return m, tui.Quit
		}
	}
	if !resolved {
		return m, nil
	}
	m.decisions[m.current.Name] = dec
	if dec.Skip {
		m.skipped++
		m.cursor++
		return m.advance()
	}
	m.step = mStepMigrating
	m.stepChangedAt = time.Now()
	return m, tui.Batch(m.spinner.Tick, m.startMigrate(m.cursor))
}

func (m migrateModel) startMigrate(index int) tui.Cmd {
	item := m.queue[index]
	dec := m.decisions[item.Name]
	machine := m.machine
	return func() tui.Msg {
		proj := item.Project
		opts := migrate.Options{
			WIP:             dec.WIP,
			StashBranch:     dec.StashBranch,
			CheckoutDefault: dec.CheckoutDefault,
			Machine:         machine,
		}
		res, err := migrate.MigrateProject(wsRoot, item.Name, &proj, opts)
		return migrateDoneMsg{index: index, project: item.Name, res: res, err: err}
	}
}

func (m migrateModel) updateMigrating(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch msg := msg.(type) {
	case tui.SpinnerTickMsg:
		var cmd tui.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case migrateDoneMsg:
		if msg.err != nil {
			m.errors = append(m.errors, migrateError{project: msg.project, err: msg.err})
		} else {
			m.successes = append(m.successes, msg.project)
			if msg.res != nil {
				_ = m.sidecar.MarkDone(msg.project, msg.res.DefaultBranch)
				_ = migrate.Save(m.sidecar)
			}
		}
		m.cursor++
		return m.advance()
	case migrateAllDoneMsg:
		m.step = mStepDone
		return m, tui.Quit
	}
	return m, nil
}
