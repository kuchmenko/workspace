package migrate

import (
	"os"
	"path/filepath"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
)

type CheckResult struct {
	Project    string
	State      string
	MainPath   string
	BarePath   string
	HasStash   bool
	IsDirty    bool
	Detached   bool
	Branch     string
	HooksFound int
}

func Check(wsRoot string, name string, proj config.Project) CheckResult {
	mainPath := filepath.Join(wsRoot, proj.Path)
	barePath := layout.BarePath(mainPath)
	res := CheckResult{Project: name, MainPath: mainPath, BarePath: barePath}

	if _, err := os.Stat(barePath); err == nil {
		res.State = "migrated"
		return res
	}
	if _, err := os.Stat(mainPath); os.IsNotExist(err) {
		res.State = "missing"
		return res
	}
	if !git.IsRepo(mainPath) {
		res.State = "not-a-repo"
		return res
	}
	res.State = "needs-migration"
	res.HasStash = git.HasStash(mainPath)
	res.IsDirty = git.IsDirty(mainPath)
	if br, _ := git.CurrentBranch(mainPath); br == "" {
		res.Detached = true
	} else {
		res.Branch = br
	}
	hooks, _ := listActiveHooks(filepath.Join(mainPath, ".git", "hooks"))
	res.HooksFound = len(hooks)
	return res
}
