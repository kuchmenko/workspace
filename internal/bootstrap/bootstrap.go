package bootstrap

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/kuchmenko/workspace/internal/sidecar"
)

type State string

const (
	StatePresent State = "present"

	StateNeedsMigrate State = "needs-migrate"

	StateBlocked State = "blocked"

	StateSelf State = "self"

	StateMissing State = "missing"
)

type PlanItem struct {
	Name    string
	Project config.Project
	State   State

	Reason string
}

type Plan struct {
	Items []PlanItem
}

func (p *Plan) Bucket(s State) []PlanItem {
	var out []PlanItem
	for _, it := range p.Items {
		if it.State == s {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (p *Plan) ToClone() []string {
	items := p.Bucket(StateMissing)
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Name
	}
	return names
}

func ScanPlan(wsRoot string, ws *config.Workspace, only []string) *Plan {
	wantOnly := map[string]bool{}
	for _, n := range only {
		wantOnly[n] = true
	}

	selfRemote := workspaceSelfRemote(wsRoot)

	plan := &Plan{}
	for name, proj := range ws.Projects {
		if proj.Status != config.StatusActive {
			continue
		}
		if len(wantOnly) > 0 && !wantOnly[name] {
			continue
		}
		item := PlanItem{Name: name, Project: proj}
		item.State, item.Reason = classify(wsRoot, proj, selfRemote)
		plan.Items = append(plan.Items, item)
	}
	sort.Slice(plan.Items, func(i, j int) bool { return plan.Items[i].Name < plan.Items[j].Name })
	return plan
}

func classify(wsRoot string, proj config.Project, selfRemote string) (State, string) {
	if selfRemote != "" && remotesEqual(proj.Remote, selfRemote) {
		return StateSelf, "this is the workspace repository itself"
	}
	mainPath := filepath.Join(wsRoot, proj.Path)
	barePath := layout.BarePath(mainPath)

	if statExists(barePath) {
		return StatePresent, ""
	}
	if statExists(mainPath) {
		if git.IsRepo(mainPath) {
			return StateNeedsMigrate, "plain checkout — run `ws migrate " + proj.Path + "`"
		}
		return StateBlocked, "non-repo files at " + mainPath
	}
	return StateMissing, ""
}

func statExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func workspaceSelfRemote(wsRoot string) string {
	root := findRepoRoot(wsRoot)
	if root == "" {
		return ""
	}
	cmd := exec.Command("git", "-C", root, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func findRepoRoot(dir string) string {
	for {
		if git.IsRepo(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func remotesEqual(a, b string) bool {
	return normalizeRemote(a) == normalizeRemote(b)
}

func normalizeRemote(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	if strings.HasPrefix(s, "git@") {
		s = strings.TrimPrefix(s, "git@")
		s = strings.Replace(s, ":", "/", 1)
	}

	for _, p := range []string{"https://", "http://", "ssh://", "git://"} {
		s = strings.TrimPrefix(s, p)
	}

	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	return strings.ToLower(s)
}

type DoneEntry struct {
	DefaultBranch string    `json:"default_branch"`
	ClonedAt      time.Time `json:"cloned_at"`
}

type Sidecar struct {
	*sidecar.Sidecar
}

func New(wsRoot string) *Sidecar {
	return &Sidecar{Sidecar: sidecar.New(wsRoot, sidecar.KindBootstrap)}
}

func Load(wsRoot string) (*Sidecar, error) {
	sc, err := sidecar.Load(wsRoot, sidecar.KindBootstrap)
	if err != nil || sc == nil {
		return nil, err
	}
	return &Sidecar{Sidecar: sc}, nil
}

func Save(sc *Sidecar) error {
	if sc == nil {
		return nil
	}
	return sidecar.Save(sc.Sidecar)
}

func Delete(wsRoot string) error {
	return sidecar.Delete(wsRoot, sidecar.KindBootstrap)
}

func IsAlive(sc *Sidecar) bool {
	if sc == nil {
		return false
	}
	return sidecar.IsAlive(sc.Sidecar)
}

func (s *Sidecar) MarkDone(name, defaultBranch string) error {
	return s.Set(name, DoneEntry{
		DefaultBranch: defaultBranch,
		ClonedAt:      time.Now().UTC(),
	})
}

func (s *Sidecar) DoneEntries() (map[string]DoneEntry, error) {
	out := make(map[string]DoneEntry, len(s.Done))
	for name := range s.Done {
		var entry DoneEntry
		if _, err := s.Get(name, &entry); err != nil {
			return nil, err
		}
		out[name] = entry
	}
	return out, nil
}
