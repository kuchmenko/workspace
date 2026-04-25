package branchprompt

// PickedMsg is emitted when the user chooses a branch, either by selecting
// from the candidate list or by typing a custom name in free-text mode.
// Callers embed the model and react to this message to unblock whatever
// code was waiting for the branch name (typically a worker goroutine
// parked on a channel).
type PickedMsg struct {
	Project string
	Branch  string
}

// CancelledMsg is emitted when the user escapes the prompt without choosing
// a branch. Callers should treat this as "skip this project" — in the
// original bootstrap flow, this surfaces as a per-project clone error.
type CancelledMsg struct {
	Project string
}
