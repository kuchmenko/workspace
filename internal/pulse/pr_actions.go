package pulse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// PR mutating actions are dispatched per-PR based on PR.Source so the
// auth token used to write matches the auth token used to read. A PR
// fetched via the App is mutated via App (HTTP / GraphQL with the ws
// OAuth user-to-server token); a PR fetched via gh is mutated via
// `gh api` shell-outs to the same endpoints. We deliberately route
// through `gh api` (not `gh pr ready` etc.) so the surface stays one
// thin layer over GitHub's REST/GraphQL — same JSON, same semantics,
// just a different transport.
//
// Routing rule:
//
//	PR.Source == SourceApp → App path (HTTP)
//	PR.Source == SourceGh  → gh path (shell)
//	PR.Source == ""        → try App, fall back to gh on transport
//	                         error (legacy callers / future sources)
//
// All actions return an error on any non-2xx response. The TUI shows
// errors in the toast slot and lets the user retry.

// httpAPIDo is the App path: REST/GraphQL via the ws OAuth token.
func httpAPIDo(method, url string, body []byte) ([]byte, int, error) {
	token, err := resolveToken()
	if err != nil {
		return nil, 0, err
	}
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return respBody, resp.StatusCode, nil
}

// ghCLIAPIDo is the gh path: same REST/GraphQL endpoint, but the
// HTTP roundtrip is delegated to `gh api` so it uses gh's token.
// Maps the standard GitHub API URL down to a gh path.
func ghCLIAPIDo(method, url string, body []byte) ([]byte, int, error) {
	if !ghAvailable() {
		return nil, 0, fmt.Errorf("gh CLI not available — cannot route action via gh")
	}
	const prefix = "https://api.github.com"
	path := strings.TrimPrefix(url, prefix)
	if path == url && url != "/graphql" {
		return nil, 0, fmt.Errorf("ghCLIAPIDo: cannot map URL %q", url)
	}
	if url == "https://api.github.com/graphql" {
		path = "graphql"
	}
	args := []string{"api", "--method", method, path}
	for _, h := range []string{
		"Accept: application/vnd.github+json",
		"X-GitHub-Api-Version: 2022-11-28",
	} {
		args = append(args, "-H", h)
	}
	if body != nil {
		args = append(args, "--input", "-")
	}
	cmd := exec.Command("gh", args...)
	if body != nil {
		cmd.Stdin = bytes.NewReader(body)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, 0, fmt.Errorf("gh api %s %s: %s", method, path, strings.TrimSpace(string(out)))
	}
	// gh api returns the body on success and exits 0; status 200 is
	// implied. Success-with-error-body still parses fine downstream.
	return out, 200, nil
}

// dispatchAPIDo picks the transport for one HTTP call based on the
// PR's Source. Used by every action below so the routing stays in
// one place.
func dispatchAPIDo(src Source, method, url string, body []byte) ([]byte, int, error) {
	switch src {
	case SourceGh:
		return ghCLIAPIDo(method, url, body)
	case SourceApp:
		return httpAPIDo(method, url, body)
	}
	// Unknown source: try App first, fall back to gh on transport
	// error (network / token missing). Don't fall back on 4xx —
	// those are real semantic errors and gh would say the same.
	body1, status, err := httpAPIDo(method, url, body)
	if err == nil {
		return body1, status, nil
	}
	if !ghAvailable() {
		return body1, status, err
	}
	return ghCLIAPIDo(method, url, body)
}

// SetDraft toggles a PR's draft state. The REST API does not expose
// draft conversion, so this uses GraphQL. The PR must have its
// NodeID populated (FetchPRs always sets it from the search response).
func SetDraft(pr PR, draft bool) error {
	if pr.NodeID == "" {
		return fmt.Errorf("PR #%d in %s has no node_id; refresh and retry", pr.Number, pr.Repo)
	}
	var mutation string
	if draft {
		mutation = `mutation($id:ID!){convertPullRequestToDraft(input:{pullRequestId:$id}){pullRequest{isDraft}}}`
	} else {
		mutation = `mutation($id:ID!){markPullRequestReadyForReview(input:{pullRequestId:$id}){pullRequest{isDraft}}}`
	}
	body, _ := json.Marshal(map[string]any{
		"query":     mutation,
		"variables": map[string]string{"id": pr.NodeID},
	})
	resp, status, err := dispatchAPIDo(pr.Source, "POST", "https://api.github.com/graphql", body)
	if err != nil {
		return err
	}
	if status != 200 {
		return describeHTTPError("graphql draft toggle", status, resp)
	}
	// GraphQL always returns 200; check for errors[] in the body.
	var out struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(resp, &out); err == nil && len(out.Errors) > 0 {
		return fmt.Errorf("graphql: %s", out.Errors[0].Message)
	}
	return nil
}

// ClosePR closes a PR without merging. PATCH .../pulls/N {state:closed}.
func ClosePR(pr PR) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d", pr.Repo, pr.Number)
	body, _ := json.Marshal(map[string]string{"state": "closed"})
	resp, status, err := dispatchAPIDo(pr.Source, "PATCH", url, body)
	if err != nil {
		return err
	}
	if status != 200 {
		return describeHTTPError("close PR", status, resp)
	}
	return nil
}

// ReopenPR re-opens a previously-closed PR.
func ReopenPR(pr PR) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d", pr.Repo, pr.Number)
	body, _ := json.Marshal(map[string]string{"state": "open"})
	resp, status, err := dispatchAPIDo(pr.Source, "PATCH", url, body)
	if err != nil {
		return err
	}
	if status != 200 {
		return describeHTTPError("reopen PR", status, resp)
	}
	return nil
}

// AddLabel adds a single label to a PR. POST /repos/{}/issues/{}/labels.
func AddLabel(pr PR, label string) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/issues/%d/labels", pr.Repo, pr.Number)
	body, _ := json.Marshal(map[string][]string{"labels": {label}})
	resp, status, err := dispatchAPIDo(pr.Source, "POST", url, body)
	if err != nil {
		return err
	}
	if status != 200 {
		return describeHTTPError("add label", status, resp)
	}
	return nil
}

// RemoveLabel removes a single label from a PR.
func RemoveLabel(pr PR, label string) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/issues/%d/labels/%s", pr.Repo, pr.Number, percentEncode(label))
	resp, status, err := dispatchAPIDo(pr.Source, "DELETE", url, nil)
	if err != nil {
		return err
	}
	if status != 200 {
		return describeHTTPError("remove label", status, resp)
	}
	return nil
}

// OpenInBrowser opens a URL in the user's default browser via the
// platform's standard tool. No GitHub API call required.
func OpenInBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("don't know how to open URLs on %s", runtime.GOOS)
	}
	return cmd.Start()
}
