package sync

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"codeberg.org/kuchmenko/workspace/internal/git"
)

const (
	ProbeWorkers = 8
	probeTimeout = 15 * time.Second
)

type ProbeStatus string

const (
	ProbeSuccess     ProbeStatus = "success"
	ProbeAccess      ProbeStatus = "auth-or-access"
	ProbeTimeout     ProbeStatus = "timeout"
	ProbeUnreachable ProbeStatus = "unreachable"
	ProbeUnsupported ProbeStatus = "unsupported"
	ProbeCanceled    ProbeStatus = "canceled"
)

type ProbeEventKind string

const (
	ProbeStarted  ProbeEventKind = "started"
	ProbeFinished ProbeEventKind = "finished"
)

type ProbeEvent struct {
	Kind       ProbeEventKind
	EndpointID string
	URL        string
	Candidate  bool
	Result     ProbeResult
}

type ProbeResult struct {
	EndpointID          string
	URL                 string
	Status              ProbeStatus
	Diagnostic          string
	Candidate           string
	CandidateStatus     ProbeStatus
	CandidateDiagnostic string
}

type SourceProbeResult struct {
	Key         string
	Status      ProbeStatus
	Diagnostics []string
}

type ProbeReport struct {
	Results []ProbeResult
	Sources []SourceProbeResult
}

func Probe(ctx context.Context, plan Plan, onEvent func(ProbeEvent)) ProbeReport {
	return ProbeWithWorkers(ctx, plan, ProbeWorkers, onEvent)
}

func ProbeWithWorkers(ctx context.Context, plan Plan, workers int, onEvent func(ProbeEvent)) ProbeReport {
	if workers < 1 {
		workers = 1
	}
	results := make([]ProbeResult, len(plan.Endpoints))
	jobs := make(chan int)
	var callbackMu sync.Mutex
	emit := func(event ProbeEvent) {
		if onEvent == nil {
			return
		}
		callbackMu.Lock()
		onEvent(event)
		callbackMu.Unlock()
	}
	var wait sync.WaitGroup
	for range min(workers, len(plan.Endpoints)) {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range jobs {
				results[index] = probeEndpoint(ctx, plan.Endpoints[index], emit)
			}
		}()
	}
	for index := range plan.Endpoints {
		select {
		case jobs <- index:
		case <-ctx.Done():
			for remaining := index; remaining < len(plan.Endpoints); remaining++ {
				results[remaining] = canceledProbe(plan.Endpoints[remaining], ctx.Err())
				emit(ProbeEvent{Kind: ProbeFinished, EndpointID: plan.Endpoints[remaining].ID, URL: plan.Endpoints[remaining].URL, Result: results[remaining]})
			}
			close(jobs)
			wait.Wait()
			return buildProbeReport(plan, results)
		}
	}
	close(jobs)
	wait.Wait()
	probeCandidates(ctx, plan, results, workers, emit)
	return buildProbeReport(plan, results)
}

func probeEndpoint(ctx context.Context, endpoint Endpoint, emit func(ProbeEvent)) ProbeResult {
	if !endpoint.Executable {
		result := ProbeResult{EndpointID: endpoint.ID, URL: endpoint.URL, Status: ProbeUnsupported, Diagnostic: endpoint.ParseError}
		emit(ProbeEvent{Kind: ProbeFinished, EndpointID: endpoint.ID, URL: endpoint.URL, Result: result})
		return result
	}
	emit(ProbeEvent{Kind: ProbeStarted, EndpointID: endpoint.ID, URL: endpoint.URL})
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	err := git.ProbeRepository(probeCtx, endpoint.URL)
	status, diagnostic := classifyProbeError(err)
	result := ProbeResult{EndpointID: endpoint.ID, URL: endpoint.URL, Status: status, Diagnostic: diagnostic}
	emit(ProbeEvent{Kind: ProbeFinished, EndpointID: endpoint.ID, URL: endpoint.URL, Result: result})
	return result
}

func canceledProbe(endpoint Endpoint, err error) ProbeResult {
	return ProbeResult{EndpointID: endpoint.ID, URL: endpoint.URL, Status: ProbeCanceled, Diagnostic: conciseDiagnostic(err)}
}

func probeCandidates(ctx context.Context, plan Plan, results []ProbeResult, workers int, emit func(ProbeEvent)) {
	type candidateJob struct {
		index int
		url   string
	}
	var jobs []candidateJob
	for index, result := range results {
		if result.Status == ProbeSuccess || result.Status == ProbeCanceled || !endpointHasOrigin(plan, plan.Endpoints[index]) {
			continue
		}
		candidate, ok := plan.Endpoints[index].Remote.SSHCandidate()
		if ok {
			jobs = append(jobs, candidateJob{index: index, url: candidate})
		}
	}
	if len(jobs) == 0 {
		return
	}
	queue := make(chan candidateJob)
	var wait sync.WaitGroup
	var resultMu sync.Mutex
	for range min(workers, len(jobs)) {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for job := range queue {
				emit(ProbeEvent{Kind: ProbeStarted, EndpointID: plan.Endpoints[job.index].ID, URL: job.url, Candidate: true})
				probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
				err := git.ProbeRepository(probeCtx, job.url)
				cancel()
				status, diagnostic := classifyProbeError(err)
				resultMu.Lock()
				results[job.index].CandidateStatus = status
				results[job.index].CandidateDiagnostic = diagnostic
				if status == ProbeSuccess {
					results[job.index].Candidate = job.url
				}
				result := results[job.index]
				resultMu.Unlock()
				emit(ProbeEvent{Kind: ProbeFinished, EndpointID: result.EndpointID, URL: job.url, Candidate: true, Result: result})
			}
		}()
	}
enqueue:
	for _, job := range jobs {
		select {
		case queue <- job:
		case <-ctx.Done():
			break enqueue
		}
	}
	close(queue)
	wait.Wait()
}

func endpointHasOrigin(plan Plan, endpoint Endpoint) bool {
	for _, targetID := range endpoint.TargetIDs {
		for _, target := range plan.Targets {
			if target.ID == targetID && (target.Role == TargetWorkspaceOrigin || target.Role == TargetProjectOrigin) {
				return true
			}
		}
	}
	return false
}

func classifyProbeError(err error) (ProbeStatus, string) {
	if err == nil {
		return ProbeSuccess, ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ProbeTimeout, "probe timed out"
	}
	if errors.Is(err, context.Canceled) {
		return ProbeCanceled, "probe canceled"
	}
	lower := strings.ToLower(err.Error())
	unreachableTerms := []string{"could not resolve", "connection refused", "connection timed out", "network is unreachable", "no route to host", "unable to access", "does not appear to be a git repository", "cannot open git-upload-pack"}
	for _, term := range unreachableTerms {
		if strings.Contains(lower, term) {
			return ProbeUnreachable, conciseDiagnostic(err)
		}
	}
	accessTerms := []string{"authentication", "permission denied", "access denied", "not found", "could not read from remote repository", "terminal prompts disabled", "403", "401"}
	for _, term := range accessTerms {
		if strings.Contains(lower, term) {
			return ProbeAccess, conciseDiagnostic(err)
		}
	}
	return ProbeAccess, conciseDiagnostic(err)
}

func conciseDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	line := strings.TrimSpace(strings.Split(err.Error(), "\n")[0])
	if len(line) > 240 {
		return line[:237] + "..."
	}
	return line
}

func buildProbeReport(plan Plan, results []ProbeResult) ProbeReport {
	report := ProbeReport{Results: results}
	byEndpoint := make(map[string]ProbeResult, len(results))
	for _, result := range results {
		byEndpoint[result.EndpointID] = result
	}
	for _, group := range plan.SourceGroups {
		source := SourceProbeResult{Key: group.Key, Status: ProbeSuccess}
		for _, endpointID := range group.EndpointIDs {
			result := byEndpoint[endpointID]
			if probeRank(result.Status) > probeRank(source.Status) {
				source.Status = result.Status
			}
			if result.Diagnostic != "" {
				source.Diagnostics = append(source.Diagnostics, result.Diagnostic)
			}
		}
		sort.Strings(source.Diagnostics)
		report.Sources = append(report.Sources, source)
	}
	return report
}

func probeRank(status ProbeStatus) int {
	switch status {
	case ProbeCanceled:
		return 5
	case ProbeTimeout:
		return 4
	case ProbeUnreachable:
		return 3
	case ProbeAccess:
		return 2
	case ProbeUnsupported:
		return 1
	default:
		return 0
	}
}

func (r ProbeReport) result(endpointID string) (ProbeResult, bool) {
	for _, result := range r.Results {
		if result.EndpointID == endpointID {
			return result, true
		}
	}
	return ProbeResult{}, false
}

func (r ProbeReport) String() string {
	return fmt.Sprintf("%d remotes across %d sources", len(r.Results), len(r.Sources))
}
