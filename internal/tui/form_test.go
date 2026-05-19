package tui

import (
	"errors"
	"testing"
)

func TestModalForm_TypingAndSubmit(t *testing.T) {
	f := NewModalForm(Cyan, "test", []Field{
		{Name: "branch", Value: ""},
	})
	for _, c := range "feat/x" {
		m, _ := f.Update(KeyMsg{Type: KeyRune, Runes: []rune{c}})
		f = m.(ModalForm)
	}
	_, cmd := f.Update(KeyMsg{Type: KeyEnter})
	if cmd == nil {
		t.Fatal("expected submit cmd")
	}
	msg := cmd()
	sub, ok := msg.(FormSubmittedMsg)
	if !ok {
		t.Fatalf("expected FormSubmittedMsg, got %T", msg)
	}
	if sub.Values["branch"] != "feat/x" {
		t.Errorf("branch = %q, want feat/x", sub.Values["branch"])
	}
}

func TestModalForm_ValidatorBlocksSubmit(t *testing.T) {
	f := NewModalForm(Cyan, "", []Field{
		{Name: "n", Value: "", Validator: func(s string) error {
			if s == "" {
				return errors.New("required")
			}
			return nil
		}},
	})
	_, cmd := f.Update(KeyMsg{Type: KeyEnter})
	if cmd != nil {
		t.Errorf("expected no cmd on validation failure, got %v", cmd())
	}
}

func TestModalForm_EscCancels(t *testing.T) {
	f := NewModalForm(Cyan, "", []Field{{Name: "n", Value: "x"}})
	_, cmd := f.Update(KeyMsg{Type: KeyEsc})
	if cmd == nil {
		t.Fatal("expected cancel cmd")
	}
	if _, ok := cmd().(FormCancelledMsg); !ok {
		t.Errorf("expected FormCancelledMsg, got %T", cmd())
	}
}
