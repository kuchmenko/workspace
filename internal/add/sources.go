package add

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"

	"codeberg.org/kuchmenko/workspace/internal/config"
	"codeberg.org/kuchmenko/workspace/internal/git"
	"codeberg.org/kuchmenko/workspace/internal/github"
	"codeberg.org/kuchmenko/workspace/internal/tui"
)

func (m AddModel) updateManual(msg tui.Msg) (tui.Model, tui.Cmd) {
	if key, ok := msg.(tui.KeyMsg); ok {
		switch key.String() {
		case "enter":
			val := strings.TrimSpace(m.manualInput.Value())
			if val == "" {
				m.manualErr = "URL is required"
				return m, nil
			}

			name := parseRepoNameFromURL(val)
			m.editFields = editFields{
				Name:     name,
				URL:      val,
				Category: config.CategoryPersonal,
				Group:    "",
				Path:     buildPath("", config.CategoryPersonal, name),
			}
			m.editFocus = 0
			m.editErr = ""
			m.transitionTo(addStateEdit)
			return m, nil
		case "esc":
			m.transitionTo(addStateBrowse)
			m.manualInput.Blur()
			return m, nil
		}
	}
	var cmd tui.Cmd
	m.manualInput, cmd = m.manualInput.Update(msg)
	return m, cmd
}

func (m AddModel) viewManual() string {
	var b strings.Builder
	b.WriteString(addTitle.Render(" Manual URL "))
	b.WriteString("\n\n")
	b.WriteString("  " + m.manualInput.View() + "\n")
	if m.manualErr != "" {
		b.WriteString("\n  " + addErr.Render(m.manualErr) + "\n")
	}
	b.WriteString("\n  " + addHelp.Render("[⏎] continue   [esc] back"))
	return b.String()
}

type GitHubSource struct {
	Provider github.Provider

	Limit int

	KnownRemotes map[string]string
}

const DefaultLimit = 50

func (*GitHubSource) Name() string { return "github" }

func (s *GitHubSource) FetchSuggestions(ctx context.Context) ([]Suggestion, error) {
	if s.Provider == nil {
		return nil, errors.New("GitHubSource: nil Provider")
	}
	limit := s.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}

	repos, err := s.Provider.SuggestRepos(ctx, limit)
	if err != nil {
		if errors.Is(err, github.ErrNotAuthed) {
			return nil, fmt.Errorf("github source: %w", err)
		}
		return nil, fmt.Errorf("github source: %w", err)
	}

	out := make([]Suggestion, 0, len(repos))
	for _, r := range repos {
		sug := Suggestion{
			Name:        r.Name,
			RemoteURL:   r.SSHURL,
			Sources:     []SourceKind{SourceGitHub},
			GhActivity:  r.Activity,
			PushedAt:    r.PushedAt,
			Description: r.Description,
			InferredGrp: r.Owner,
		}

		if s.KnownRemotes != nil {
			if p, ok := s.KnownRemotes[strings.ToLower(r.FullName)]; ok && p != "" {
				sug.RegisteredPath = p
			}
		}
		out = append(out, sug)
	}
	return out, nil
}

var ErrClipboardUnavailable = errors.New("no clipboard tool available")

type ClipboardReader interface {
	Read(ctx context.Context) (string, error)
}

var DefaultClipboardReader ClipboardReader = systemClipboardReader{}

type systemClipboardReader struct{}

func (systemClipboardReader) Read(ctx context.Context) (string, error) {
	tool, args, err := detectClipboard()
	if err != nil {
		return "", err
	}
	return runClipboardTool(ctx, tool, args...)
}

func detectClipboard() (string, []string, error) {
	switch runtime.GOOS {
	case "linux":
		return detectLinuxClipboard()
	case "darwin":
		return detectDarwinClipboard()
	}
	return "", nil, ErrClipboardUnavailable
}

func detectLinuxClipboard() (string, []string, error) {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if p, err := exec.LookPath("wl-paste"); err == nil {
			return p, []string{"--no-newline"}, nil
		}
	}
	if os.Getenv("DISPLAY") != "" {
		if p, err := exec.LookPath("xclip"); err == nil {
			return p, []string{"-o", "-selection", "clipboard"}, nil
		}
	}
	return "", nil, ErrClipboardUnavailable
}

func detectDarwinClipboard() (string, []string, error) {
	if p, err := exec.LookPath("pbpaste"); err == nil {
		return p, nil, nil
	}
	return "", nil, ErrClipboardUnavailable
}

func runClipboardTool(ctx context.Context, cmd string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, cmd, args...)
	out, err := c.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		exitErr := &exec.ExitError{}
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("%s: %s", cmd, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("%s: %w", cmd, err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

type ClipboardSource struct {
	Reader ClipboardReader

	AllowedHostsExtra []string
}

func (*ClipboardSource) Name() string { return "clipboard" }

func (s *ClipboardSource) FetchSuggestions(ctx context.Context) ([]Suggestion, error) {
	r := s.Reader
	if r == nil {
		r = DefaultClipboardReader
	}

	raw, err := r.Read(ctx)
	if err != nil {
		if errors.Is(err, ErrClipboardUnavailable) {
			return nil, nil
		}
		return nil, err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if !looksLikeGitURL(raw, s.allowedHosts()) {
		return nil, nil
	}

	name := git.ParseRepoName(raw)
	return []Suggestion{{
		Name:      name,
		RemoteURL: raw,
		Sources:   []SourceKind{SourceClipboard},
	}}, nil
}

func (s *ClipboardSource) allowedHosts() map[string]bool {
	hosts := map[string]bool{
		"github.com":    true,
		"gitlab.com":    true,
		"bitbucket.org": true,
		"codeberg.org":  true,
	}
	if env := os.Getenv("WS_GIT_HOSTS"); env != "" {
		for _, h := range strings.Split(env, ":") {
			h = strings.ToLower(strings.TrimSpace(h))
			if h != "" {
				hosts[h] = true
			}
		}
	}
	for _, h := range s.AllowedHostsExtra {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			hosts[h] = true
		}
	}
	return hosts
}

var shorthandRegex = regexp.MustCompile(
	`^[a-zA-Z0-9._-]+@([a-zA-Z0-9.-]+):([a-zA-Z0-9._/-]+?)(?:\.git)?/?$`,
)

var ownerRepoPath = regexp.MustCompile(
	`^/[a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+/?$`,
)

func looksLikeGitURL(s string, allowedHosts map[string]bool) bool {
	s = strings.TrimSpace(s)

	if strings.ContainsAny(s, " \t\n\r") {
		return false
	}

	if m := shorthandRegex.FindStringSubmatch(s); m != nil {
		host := strings.ToLower(m[1])

		if allowedHosts[host] {
			return true
		}

		return true
	}

	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "http", "ssh", "git":

	default:
		return false
	}
	if u.Host == "" {
		return false
	}

	host := strings.ToLower(u.Host)

	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}

	pathTrimmed := strings.TrimSuffix(u.Path, "/")

	if strings.HasSuffix(pathTrimmed, ".git") {
		return true
	}

	if allowedHosts[host] {
		if ownerRepoPath.MatchString(pathTrimmed+"/") || ownerRepoPath.MatchString(pathTrimmed) {
			return true
		}
		return false
	}

	if ownerRepoPath.MatchString(pathTrimmed) || ownerRepoPath.MatchString(pathTrimmed+"/") {
		return true
	}
	return false
}

func (m AddModel) startCloneJob(idx int) tui.Cmd {
	if idx >= len(m.queue) {
		return func() tui.Msg { return allClonesDoneMsg{} }
	}
	job := m.queue[idx]
	return func() tui.Msg {
		opts := Options{
			URLs:      []string{job.URL},
			Name:      job.Name,
			Category:  job.Category,
			Group:     job.Group,
			WsRoot:    m.wsRoot,
			Workspace: m.ws,
			Save:      m.saveFn,
			Mode:      ModeHeadless,
			NoClone:   job.FromDisk != "",
		}

		regRes, err := Register(opts, job.URL)
		out := cloneDoneMsg{idx: idx}
		if err != nil {
			if errors.Is(err, ErrAlreadyRegistered) {
				out.skipped = &SkipReason{URL: job.URL, Reason: err.Error()}
			} else if errors.Is(err, git.ErrNeedsBootstrap) {
				out.err = fmt.Errorf("%s: default branch ambiguous (run `ws bootstrap %s` after add)", job.Name, job.Name)
			} else {
				out.err = err
			}
		} else if regRes != nil {
			out.project = regRes.Project
		}
		return out
	}
}

func (m AddModel) updateCloning(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch msg := msg.(type) {
	case tui.SpinnerTickMsg:
		var cmd tui.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case cloneDoneMsg:
		switch {
		case msg.err != nil:
			m.errors = append(m.errors, msg.err)
		case msg.skipped != nil:
			m.skipped = append(m.skipped, *msg.skipped)
		default:
			m.added = append(m.added, msg.project)
		}
		m.currentIdx = msg.idx + 1
		if m.currentIdx >= len(m.queue) {
			m.transitionTo(addStateDone)
			if m.standalone {
				return m, tui.Sequence(emit(m.doneMsg()), tui.Quit)
			}
			return m, emit(m.doneMsg())
		}
		return m, m.startCloneJob(m.currentIdx)
	case needsBranchMsg:

		m.branchPrompt = tui.NewBranchPromptModel(msg.project, msg.candidates)
		m.branchAnswer = msg.answer
		m.transitionTo(addStateBranchPrompt)
		return m, nil
	case allClonesDoneMsg:
		m.transitionTo(addStateDone)
		if m.standalone {
			return m, tui.Sequence(emit(m.doneMsg()), tui.Quit)
		}
		return m, emit(m.doneMsg())
	}
	return m, nil
}

func (m AddModel) viewCloning() string {
	var b strings.Builder
	b.WriteString(addTitle.Render(" Cloning "))
	b.WriteString("\n\n")
	total := len(m.queue)
	done := m.currentIdx
	fmt.Fprintf(&b, "  %d / %d\n\n", done, total)
	if m.currentIdx < total {
		j := m.queue[m.currentIdx]
		fmt.Fprintf(&b, "  %s %s\n", m.spinner.View(), j.Name)
		fmt.Fprintf(&b, "    %s\n", addDim.Render(j.Path))
	}
	if len(m.errors) > 0 {
		fmt.Fprintf(&b, "\n  %s %d failed\n", addErr.Render("✗"), len(m.errors))
	}
	b.WriteString("\n  " + addHelp.Render("[ctrl+c] abort"))
	return b.String()
}

func (m AddModel) updateBranchPrompt(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch msg := msg.(type) {
	case tui.BranchPromptPickedMsg:
		m.resolveBranch(msg.Branch, nil)
		m.transitionTo(addStateCloning)
		return m, nil
	case tui.BranchPromptCancelledMsg:
		m.resolveBranch("", errors.New("user canceled branch selection"))
		m.transitionTo(addStateCloning)
		return m, nil
	}
	var cmd tui.Cmd
	m.branchPrompt, cmd = m.branchPrompt.Update(msg)
	return m, cmd
}

func (m *AddModel) resolveBranch(branch string, err error) {
	if m.branchAnswer != nil {
		m.branchAnswer <- branchAnswer{branch: branch, err: err}
		m.branchAnswer = nil
	}
}

func (m AddModel) updateDone(msg tui.Msg) (tui.Model, tui.Cmd) {
	if _, ok := msg.(tui.KeyMsg); ok {
		if m.standalone {
			return m, tui.Quit
		}
	}
	return m, nil
}

func (m AddModel) viewDone() string {
	var b strings.Builder
	b.WriteString(addTitle.Render(" Done "))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "  %s %d added\n", addCheck.Render("✓"), len(m.added))
	if len(m.skipped) > 0 {
		fmt.Fprintf(&b, "  %s %d skipped\n", addDim.Render("⊘"), len(m.skipped))
	}
	if len(m.errors) > 0 {
		fmt.Fprintf(&b, "  %s %d errored\n", addErr.Render("✗"), len(m.errors))
		b.WriteString("\n")
		for _, e := range m.errors {
			fmt.Fprintf(&b, "    %s\n", addDim.Render(e.Error()))
		}
	}
	b.WriteString("\n  " + addHelp.Render("[any key] exit"))
	return b.String()
}

func (m AddModel) handleSourceDone(msg sourceDoneMsg) (tui.Model, tui.Cmd) {
	m.sourcesDone++
	m.sourceOutcomes = append(m.sourceOutcomes, SourceOutcome{
		Name:     msg.name,
		Count:    len(msg.items),
		Duration: msg.took,
		Err:      msg.err,
	})
	if msg.err == nil && len(msg.items) > 0 {
		merged := mergeSuggestions([][]Suggestion{m.allSuggestions, msg.items})
		sortByRelevance(merged)
		m.allSuggestions = merged

		if m.cursor >= len(m.allSuggestions) && len(m.allSuggestions) > 0 {
			m.cursor = len(m.allSuggestions) - 1
		}
	}

	if m.state == addStateGathering {
		switch {
		case len(m.allSuggestions) > 0:
			m.transitionTo(addStateBrowse)
		case m.sourcesDone >= len(m.sources):
			m.transitionTo(addStateBrowseEmpty)
		}
	}
	return m, nil
}

func (m AddModel) updateGathering(msg tui.Msg) (tui.Model, tui.Cmd) {
	if _, ok := msg.(tui.SpinnerTickMsg); ok {
		var cmd tui.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m AddModel) viewGathering() string {
	var b strings.Builder
	b.WriteString(addTitle.Render(" Add project — gathering "))
	b.WriteString("\n\n")
	b.WriteString("  " + m.spinner.View() + " probing sources")
	if m.sourcesDone > 0 {
		fmt.Fprintf(&b, " %s", addDim.Render(fmt.Sprintf("(%d/%d done)", m.sourcesDone, len(m.sources))))
	}
	b.WriteString("\n\n")

	if len(m.sourceOutcomes) > 0 {
		b.WriteString("  ")
		b.WriteString(renderSourceChipsLive(m.sourceOutcomes))
		b.WriteString("\n\n")
	}
	b.WriteString("  " + addHelp.Render("[ctrl+c] cancel"))
	return b.String()
}
