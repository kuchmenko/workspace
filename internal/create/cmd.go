package create

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/add"
	"github.com/kuchmenko/workspace/internal/config"
)

type ownersLoadedMsg struct{ owners []Owner }
type ownersErrMsg struct{ err error }
type createDoneMsg struct{ result *Result }
type createErrMsg struct{ err error }

func (m CreateModel) fetchOwnersCmd() tea.Cmd {
	runner := m.opts.GHRunner
	if runner == nil {
		runner = realGHRunner{}
	}
	return func() tea.Msg {
		owners, err := ListOwners(runner)
		if err != nil {
			return ownersErrMsg{err: err}
		}
		return ownersLoadedMsg{owners: owners}
	}
}

func (m CreateModel) createCmd() tea.Cmd {
	runner := m.opts.GHRunner
	if runner == nil {
		runner = realGHRunner{}
	}

	owner := m.currentOwner()
	name := strings.TrimSpace(m.nameInput.Value())
	desc := strings.TrimSpace(m.descInput.Value())
	visibility := m.visibilities[m.visIdx]
	category := m.categories[m.catIdx]
	group := strings.TrimSpace(m.groupInput.Value())

	wsRoot := m.opts.WsRoot
	ws := m.opts.Workspace
	saveFn := m.opts.Save
	if saveFn == nil {
		saveFn = func(w *config.Workspace) error { return config.Save(wsRoot, w) }
	}
	projectName := m.opts.ProjectName
	if projectName == "" {
		projectName = name
	}

	return func() tea.Msg {
		if _, err := CreateRepo(runner, CreateRepoOptions{
			Owner:       owner,
			Name:        name,
			Visibility:  visibility,
			Description: desc,
			AddReadme:   true,
		}); err != nil {
			return createErrMsg{err: fmt.Errorf("create repo: %w", err)}
		}

		urlFor := m.opts.URLFor
		if urlFor == nil {
			urlFor = SSHURLFromOwnerRepo
		}
		sshURL := urlFor(owner, name)
		regOpts := add.Options{
			Category:  category,
			Group:     group,
			Name:      projectName,
			WsRoot:    wsRoot,
			Workspace: ws,
			Save:      saveFn,
		}
		regRes, err := add.Register(regOpts, sshURL)
		if err != nil {
			return createErrMsg{
				err: fmt.Errorf("repo created on GitHub at %s but register failed: %w", sshURL, err),
			}
		}

		return createDoneMsg{
			result: &Result{
				Project: regRes.Project,
				Name:    regRes.Name,
				URL:     sshURL,
				Cloned:  regRes.Cloned,
			},
		}
	}
}
