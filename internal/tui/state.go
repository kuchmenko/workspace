package tui

type Steppable interface {
	Model
	IsDone() bool
}

type Stepper struct {
	steps []Steppable
	idx   int
}

func NewStepper(steps ...Steppable) Stepper {
	return Stepper{steps: steps}
}

func (s Stepper) Init() Cmd {
	if len(s.steps) == 0 {
		return nil
	}
	return s.steps[0].Init()
}

func (s Stepper) Update(msg Msg) (Model, Cmd) {
	if s.idx >= len(s.steps) {
		return s, nil
	}
	updated, cmd := s.steps[s.idx].Update(msg)
	s.steps[s.idx] = updated.(Steppable)
	if s.steps[s.idx].IsDone() && s.idx+1 < len(s.steps) {
		s.idx++
		next := s.steps[s.idx].Init()
		cmd = Batch(cmd, next)
	}
	return s, cmd
}

func (s Stepper) View() string {
	if s.idx >= len(s.steps) {
		return ""
	}
	return s.steps[s.idx].View()
}

func (s Stepper) Current() int { return s.idx }
func (s Stepper) Done() bool {
	return s.idx >= len(s.steps) || (s.idx == len(s.steps)-1 && s.steps[s.idx].IsDone())
}
