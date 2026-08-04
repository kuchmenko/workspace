package agent

import (
	"strings"
	"testing"
)

func TestRepositoryLabelsAreSanitizedWhenRendered(t *testing.T) {
	tests := []struct {
		name  string
		label string
		bad   []string
	}{
		{name: "CSI", label: "safe\x1b[31mred", bad: []string{"[31m", "\x1b"}},
		{name: "OSC clipboard", label: "safe\x1b]52;c;YXR0YWNr\x07tail", bad: []string{"52;c", "YXR0YWNr", "\x1b", "\x07"}},
		{name: "newline and tab", label: "safe\nnext\tcolumn", bad: []string{"\n", "\t"}},
		{name: "bidi", label: "safe\u202eevil\u2066tail", bad: []string{"\u202e", "\u2066"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Model{expanded: map[string]bool{}}
			group := m.renderGroup(listItem{kind: KindGroup, workspaceRoot: "/ws", group: tt.label}, false, false, false, 0, 80, false)
			project := m.renderProject(listItem{kind: KindProject, project: &Project{Name: tt.label}}, false, false, false, 0, 80, false)
			sheet := (&sheet{mode: sheetGroup, group: tt.label}).title()
			for _, rendered := range []string{group, project, sheet} {
				for _, bad := range tt.bad {
					if strings.Contains(rendered, bad) {
						t.Fatalf("rendered label %q contains %q", rendered, bad)
					}
				}
			}
		})
	}
}

func TestPresentationDoesNotChangeIdentifiers(t *testing.T) {
	name := "project\x1b[31m"
	p := Project{ID: name, Name: name}
	_ = presentLabel(p.Name)
	if p.ID != name || p.Name != name {
		t.Fatalf("identifier changed: %#v", p)
	}
}
