package branchprompt

type PickedMsg struct {
	Project string
	Branch  string
}

type CancelledMsg struct {
	Project string
}
