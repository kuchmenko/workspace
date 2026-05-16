package add

import (
	"time"

	"github.com/kuchmenko/workspace/internal/config"
)

// AddDoneMsg signals that the model has finished its work. Standalone
// callers consume this and quit; embedded callers consume it to
// transition back to their parent state.
type AddDoneMsg struct {
	Added   []config.Project
	Skipped []SkipReason
	Errors  []error
}

// cloneDoneMsg is posted after each Register call in the cloning queue.
type cloneDoneMsg struct {
	idx     int
	project config.Project
	skipped *SkipReason
	err     error
}

// allClonesDoneMsg signals the cloning loop reached the end of the queue.
type allClonesDoneMsg struct{}

// needsBranchMsg is the bridge from a clone goroutine that hit
// clone.ErrNeedsBootstrap. The TUI switches into branchPrompt state,
// the user picks, and the answer flows back via the channel.
type needsBranchMsg struct {
	project    string
	candidates []string
	answer     chan branchAnswer
}

// sourceDoneMsg lands on AddModel.Update each time a single source
// finishes its FetchSuggestions call. Multiple sourceDoneMsgs are
// expected per session (one per source).
type sourceDoneMsg struct {
	name  string
	items []Suggestion
	err   error
	took  time.Duration
}
