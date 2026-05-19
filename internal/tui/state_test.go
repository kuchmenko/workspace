package tui

import "testing"

type stepStub struct {
	done bool
	view string
}

func (s stepStub) Init() Cmd    { return nil }
func (s stepStub) View() string { return s.view }
func (s stepStub) IsDone() bool { return s.done }
func (s stepStub) Update(msg Msg) (Model, Cmd) {
	if _, ok := msg.(KeyMsg); ok {
		s.done = true
	}
	return s, nil
}

func TestStepper_AdvancesOnDone(t *testing.T) {
	s := NewStepper(stepStub{view: "step1"}, stepStub{view: "step2"})
	if s.Current() != 0 {
		t.Fatalf("initial idx = %d", s.Current())
	}
	if s.View() != "step1" {
		t.Errorf("initial view = %q", s.View())
	}
	m, _ := s.Update(KeyMsg{Type: KeyEnter})
	s = m.(Stepper)
	if s.Current() != 1 {
		t.Errorf("after key: idx = %d, want 1", s.Current())
	}
	if s.View() != "step2" {
		t.Errorf("after key: view = %q, want step2", s.View())
	}
}
