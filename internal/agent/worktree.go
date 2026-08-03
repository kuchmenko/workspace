package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/kuchmenko/workspace/internal/repo"
	"github.com/kuchmenko/workspace/internal/tui"
)

type Worktree struct {
	Path   string
	Branch string
	IsMain bool
	Dirty  bool
	Ahead  int
}

func explorerMachineName() (string, error) {
	machine, err := config.LoadMachineConfig()
	if err != nil {
		return "", fmt.Errorf("load machine config: %w", err)
	}
	if machine == nil || machine.MachineName == "" {
		return "", fmt.Errorf("machine name is not configured")
	}
	return machine.MachineName, nil
}

func worktreeDisplayName(wt Worktree) string {
	if wt.IsMain {
		return "main"
	}
	if strings.HasPrefix(wt.Branch, "wt/") {
		parts := strings.SplitN(wt.Branch, "/", 3)
		if len(parts) == 3 {
			return parts[2]
		}
	}
	if wt.Branch != "" {
		return wt.Branch
	}
	return filepath.Base(wt.Path)
}

type WorktreeCache struct {
	data map[string][]Worktree
}

func NewWorktreeCache() *WorktreeCache {
	return &WorktreeCache{data: make(map[string][]Worktree)}
}

func (c *WorktreeCache) Get(mainPath string) []Worktree {
	if wts, ok := c.data[mainPath]; ok {
		return wts
	}
	wts := LoadWorktrees(mainPath)
	c.data[mainPath] = wts
	return wts
}

func (c *WorktreeCache) Invalidate(mainPath string) {
	delete(c.data, mainPath)
}

func LoadWorktrees(mainPath string) []Worktree {
	barePath := layout.BarePath(mainPath)
	if _, err := os.Stat(barePath); err != nil {
		return []Worktree{{Path: mainPath, Branch: "", IsMain: true, Dirty: git.IsDirty(mainPath)}}
	}

	wts, err := git.WorktreeList(barePath)
	if err != nil {
		return []Worktree{{Path: mainPath, Branch: "", IsMain: true, Dirty: git.IsDirty(mainPath)}}
	}

	var result []Worktree
	for _, wt := range wts {
		if wt.Bare {
			continue
		}
		w := Worktree{
			Path:   wt.Path,
			Branch: wt.Branch,
			IsMain: wt.Path == mainPath,
			Dirty:  git.IsDirty(wt.Path),
		}
		ahead, _, _ := git.AheadBehind(wt.Path, wt.Branch)
		w.Ahead = ahead
		result = append(result, w)
	}
	return result
}

func (m *Model) updateNewWorktree(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	key := msg.String()

	switch key {
	case "esc":
		m.mode = viewList
		return m, nil
	case "tab", "down":
		m.wtField = (m.wtField + 1) % 2
		return m, nil
	case "shift+tab", "up":
		m.wtField = (m.wtField + 1) % 2
		return m, nil
	case "enter":
		if m.wtField == 1 {
			return m.executeNewWorktree()
		}
		m.wtField = (m.wtField + 1) % 2
		return m, nil
	case "backspace":
		if m.wtField == 0 && len(m.wtBranch) > 0 {
			m.wtBranch = m.wtBranch[:len(m.wtBranch)-1]
		}
		return m, nil
	default:
		if m.wtField == 0 && len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			m.wtBranch += key
		}
	}
	return m, nil
}

func (m *Model) executeNewWorktree() (tui.Model, tui.Cmd) {
	branch := strings.TrimSpace(m.wtBranch)
	if branch == "" {
		return m, nil
	}

	wsRoot := m.workspaceRootFor(m.popupProj)
	machine, err := explorerMachineName()
	if err == nil {
		_, err = repo.AddWorktree(repo.WorktreeAddOptions{WorkspaceRoot: wsRoot, Project: m.popupProj.ID, Branch: branch, Machine: machine})
	}
	if err != nil {
		m.statusMsg = err.Error()
		m.mode = viewList
		return m, nil
	}
	m.wtCache.Invalidate(m.popupProj.Path)

	m.mode = viewList
	m.rebuildItems()
	m.ensureVisible()
	m.statusMsg = "worktree created"
	return m, nil
}

func (m *Model) viewNewWorktree() string {
	p := m.popupProj
	popupW := 50
	if m.width < 56 {
		popupW = m.width - 6
	}
	innerW := popupW - 6

	var lines []string
	lines = append(lines, popupTitleStyle.Width(innerW).Render(fmt.Sprintf("%s New worktree for %s", iconWorktree, p.Name)))
	lines = append(lines, "")

	branchLabel := "  Branch name:"
	branchVal := m.wtBranch + "█"
	if m.wtField != 0 {
		branchVal = m.wtBranch
		if branchVal == "" {
			branchVal = "(required)"
		}
	}
	if m.wtField == 0 {
		lines = append(lines, popupSelectedStyle.Width(innerW).Render(branchLabel))
		lines = append(lines, popupSelectedStyle.Width(innerW).Render("  "+branchVal))
	} else {
		lines = append(lines, popupItemStyle.Width(innerW).Render(branchLabel))
		lines = append(lines, popupDimStyle.Width(innerW).Render("  "+branchVal))
	}
	if branch := strings.TrimSpace(m.wtBranch); branch != "" {
		pathPreview := fmt.Sprintf("  → dir: %s-wt-<machine>-%s", p.Name, layout.SlugifyBranch(branch))
		lines = append(lines, popupDimStyle.Width(innerW).Render(pathPreview))
	}
	lines = append(lines, "")

	confirmLabel := "  → Create worktree"
	if m.wtField == 1 {
		lines = append(lines, popupSelectedStyle.Width(innerW).Render(confirmLabel))
	} else {
		lines = append(lines, popupItemStyle.Width(innerW).Render(confirmLabel))
	}

	lines = append(lines, "")
	lines = append(lines, popupDimStyle.Width(innerW).Render("tab:next  enter:confirm  esc:back"))

	content := strings.Join(lines, "\n")
	popup := popupBorderStyle.Render(content)

	return tui.Place(m.width, m.height, tui.Center, tui.Center, popup,
		tui.WithWhitespaceBackground(tui.Color("234")))
}
