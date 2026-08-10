package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/kuchmenko/workspace/internal/metrics"
	"github.com/kuchmenko/workspace/internal/repo"
	"github.com/kuchmenko/workspace/internal/tui"
)

type Worktree struct {
	Path         string
	Branch       string
	HEAD         string
	IsMain       bool
	Dirty        bool
	Unknown      bool
	Ahead        int
	Behind       int
	LastActiveAt time.Time
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
	details   map[string][]Worktree
	inventory map[string][]Worktree
}

func NewWorktreeCache() *WorktreeCache {
	return &WorktreeCache{details: make(map[string][]Worktree), inventory: make(map[string][]Worktree)}
}

func (c *WorktreeCache) Get(mainPath string) ([]Worktree, error) {
	if wts, ok := c.details[mainPath]; ok {
		return wts, nil
	}
	wts, err := LoadWorktrees(mainPath)
	if err != nil {
		return nil, err
	}
	c.details[mainPath] = wts
	c.inventory[mainPath] = wts
	return wts, nil
}

func (c *WorktreeCache) SeedInventory(mainPath string, wts []Worktree) {
	c.inventory[mainPath] = wts
}

func (c *WorktreeCache) SeedDetails(mainPath string, wts []Worktree) {
	c.details[mainPath] = wts
	c.inventory[mainPath] = wts
}

func (c *WorktreeCache) Inventory(mainPath string) []Worktree {
	if wts, ok := c.inventory[mainPath]; ok {
		return wts
	}
	wts, err := LoadWorktreeInventory(mainPath)
	if err != nil {
		return nil
	}
	c.inventory[mainPath] = wts
	return wts
}

func (c *WorktreeCache) Invalidate(mainPath string) {
	delete(c.details, mainPath)
	delete(c.inventory, mainPath)
}

func LoadWorktreeInventory(mainPath string) ([]Worktree, error) {
	barePath := layout.BarePath(mainPath)
	if _, err := os.Stat(barePath); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		if _, err := os.Stat(mainPath); err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		lastActiveAt, _ := git.LastCommitTime(mainPath)
		return []Worktree{{Path: mainPath, IsMain: true, LastActiveAt: lastActiveAt}}, nil
	}
	wts, err := git.WorktreeList(barePath)
	if err != nil {
		return nil, err
	}
	return buildWorktreeInventory(mainPath, barePath, wts)
}

func buildWorktreeInventory(mainPath, repoPath string, listed []git.Worktree) ([]Worktree, error) {
	commits := make([]string, 0, len(listed))
	for _, wt := range listed {
		if !wt.Bare && wt.HEAD != "" {
			commits = append(commits, wt.HEAD)
		}
	}
	times, err := git.CommitTimes(repoPath, commits)
	result := make([]Worktree, 0, len(listed))
	for _, wt := range listed {
		if wt.Bare {
			continue
		}
		result = append(result, Worktree{Path: wt.Path, Branch: wt.Branch, HEAD: wt.HEAD, IsMain: wt.Path == mainPath, LastActiveAt: times[wt.HEAD]})
	}
	return result, err
}

func LoadWorktrees(mainPath string) ([]Worktree, error) {
	barePath := layout.BarePath(mainPath)
	if _, err := os.Stat(barePath); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		if _, err := os.Stat(mainPath); err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		modified, err := git.WorktreeModified(mainPath)
		return []Worktree{{Path: mainPath, Branch: "", HEAD: git.RevParse(mainPath, "HEAD"), IsMain: true, Dirty: modified, Unknown: err != nil}}, nil
	}

	wts, err := git.WorktreeList(barePath)
	if err != nil {
		return nil, err
	}

	var result []Worktree
	for _, wt := range wts {
		if wt.Bare {
			continue
		}
		modified, statusErr := git.WorktreeModified(wt.Path)
		w := Worktree{
			Path:    wt.Path,
			Branch:  wt.Branch,
			HEAD:    wt.HEAD,
			IsMain:  wt.Path == mainPath,
			Dirty:   modified,
			Unknown: statusErr != nil,
		}
		if value, err := git.LastCommitTime(wt.Path); err == nil {
			w.LastActiveAt = value
		}
		w.Ahead, w.Behind, _ = git.AheadBehind(wt.Path, wt.Branch)
		result = append(result, w)
	}
	return result, nil
}

func (m *Model) updateNewWorktree(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	key := msg.String()

	switch key {
	case "q":
		if m.wtField == 0 {
			var cmd tui.Cmd
			m.wtBranch, cmd = m.wtBranch.Update(msg)
			return m, cmd
		}
		fallthrough
	case "esc":
		m.wtBranch.Blur()
		m.restoreFormOrigin()
		return m, nil
	case "tab", "down":
		m.wtField = (m.wtField + 1) % 2
		if m.wtField == 0 {
			return m, m.wtBranch.Focus()
		}
		m.wtBranch.Blur()
		return m, nil
	case "shift+tab", "up":
		m.wtField = (m.wtField + 1) % 2
		if m.wtField == 0 {
			return m, m.wtBranch.Focus()
		}
		m.wtBranch.Blur()
		return m, nil
	case "enter":
		if m.wtField == 1 {
			return m.executeNewWorktree()
		}
		m.wtField = (m.wtField + 1) % 2
		m.wtBranch.Blur()
		return m, nil
	default:
		if m.wtField == 0 {
			var cmd tui.Cmd
			m.wtBranch, cmd = m.wtBranch.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func (m *Model) executeNewWorktree() (tui.Model, tui.Cmd) {
	branch := strings.TrimSpace(m.wtBranch.Value())
	if branch == "" {
		return m, nil
	}

	wsRoot := m.workspaceRootFor(m.popupProj)
	projectID, projectName := m.popupProj.ID, m.popupProj.Name
	m.wtBranch.Blur()
	m.mode = viewList
	projectPath := m.popupProj.Path
	cmd := m.submitJob("create "+projectName+"/"+branch, 1, func(ctx *jobContext) jobResult {
		var outcome targetOutcome
		var options repo.WorktreeAddOptions
		var addResult *repo.WorktreeAddResult
		ctx.withProject(wsRoot, projectID, projectPath, func() {
			ctx.progress(branch)
			machine, err := explorerMachineName()
			if err == nil {
				options = repo.WorktreeAddOptions{WorkspaceRoot: wsRoot, Project: projectID, Branch: branch, Machine: machine}
				addResult, err = repo.AddWorktreeCheckout(options)
			}
			if err != nil {
				if addResult != nil {
					outcome = targetOutcome{Target: branch, Kind: targetPartial, Detail: err.Error()}
				} else {
					outcome = targetOutcome{Target: branch, Kind: targetFailed, Detail: err.Error()}
				}
			} else {
				outcome = targetOutcome{Target: branch, Kind: targetSuccess, Detail: "created"}
			}
			ctx.withRegistry(wsRoot, func() {
				if outcome.Kind == targetSuccess {
					if err := repo.RegisterWorktree(options, addResult); err != nil {
						outcome = targetOutcome{Target: branch, Kind: targetPartial, Detail: err.Error()}
					}
				}
				child := jobResult{Outcomes: []targetOutcome{outcome}}
				if addResult != nil {
					child.AffectedProjects = []ProjectIdentity{{wsRoot, projectID}}
				}
				ctx.finishChild(child, addResult != nil)
			})
		})
		metrics.RecordExplorerWorktreeCreated()
		result := jobResult{Summary: "worktree created", Outcomes: []targetOutcome{outcome}}
		if outcome.Kind == targetSuccess || outcome.Kind == targetPartial {
			result.AffectedProjects = []ProjectIdentity{{wsRoot, projectID}}
		}
		if outcome.Kind != targetSuccess {
			result.Error = outcome.Detail
		}
		return result
	})
	m.restoreFormOrigin()
	return m, cmd
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
	branchVal := m.wtBranch.View()
	if m.wtField != 0 {
		branchVal = m.wtBranch.Value()
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
	if branch := strings.TrimSpace(m.wtBranch.Value()); branch != "" {
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
	lines = append(lines, popupDimStyle.Width(innerW).Render("tab:next  enter:confirm  q:back"))

	content := strings.Join(lines, "\n")
	popup := popupBorderStyle.Render(content)

	return tui.Place(m.width, m.height, tui.Center, tui.Center, popup,
		tui.WithWhitespaceBackground(tui.Color("234")))
}
