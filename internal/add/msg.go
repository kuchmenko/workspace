package add

import (
	"time"

	"github.com/kuchmenko/workspace/internal/config"
)

type AddDoneMsg struct {
	Added   []config.Project
	Skipped []SkipReason
	Errors  []error
}

type cloneDoneMsg struct {
	idx     int
	project config.Project
	skipped *SkipReason
	err     error
}

type allClonesDoneMsg struct{}

type needsBranchMsg struct {
	project    string
	candidates []string
	answer     chan branchAnswer
}

type sourceDoneMsg struct {
	name  string
	items []Suggestion
	err   error
	took  time.Duration
}
