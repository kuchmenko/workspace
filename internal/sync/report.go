package sync

type EventKind string

const (
	EventStarted    EventKind = "started"
	EventWorkspace  EventKind = "workspace"
	EventProject    EventKind = "project"
	EventMirror     EventKind = "mirror"
	EventConflict   EventKind = "conflict"
	EventSkipped    EventKind = "skipped"
	EventConversion EventKind = "conversion"
	EventCanceled   EventKind = "canceled"
)

type ResultStatus string

const (
	ResultRunning  ResultStatus = "running"
	ResultSuccess  ResultStatus = "success"
	ResultFailed   ResultStatus = "failed"
	ResultSkipped  ResultStatus = "skipped"
	ResultCanceled ResultStatus = "canceled"
)

type SkipReason string

const (
	SkipExcluded    SkipReason = "excluded"
	SkipPlanChanged SkipReason = "plan-changed"
	SkipSidecar     SkipReason = "sidecar-active"
	SkipCanceled    SkipReason = "canceled"
	SkipState       SkipReason = "state"
)

type Event struct {
	Kind       EventKind
	Status     ResultStatus
	Project    string
	TargetID   string
	Mirror     string
	Branch     string
	Operation  string
	Reason     SkipReason
	Diagnostic string
}

type OperationResult struct {
	Status     ResultStatus
	Operation  string
	Project    string
	TargetID   string
	Mirror     string
	Branch     string
	Reason     SkipReason
	Diagnostic string
}

type ConversionResult struct {
	TargetID   string
	Project    string
	From       string
	To         string
	Status     ResultStatus
	Diagnostic string
}

type Report struct {
	Workspace   []OperationResult
	Projects    []OperationResult
	Mirrors     []OperationResult
	Conflicts   []OperationResult
	Conversions []ConversionResult
	Events      []Event
	Canceled    bool
}

func (r *Report) add(event Event, onEvent func(Event)) {
	r.Events = append(r.Events, event)
	if onEvent != nil {
		onEvent(event)
	}
}

func (r *Report) start(event Event, onEvent func(Event)) {
	event.Kind = EventStarted
	event.Status = ResultRunning
	r.add(event, onEvent)
}
