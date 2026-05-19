package create

import "github.com/kuchmenko/workspace/internal/config"

type Mode int

const (
	ModeAuto Mode = iota

	ModeHeadless

	ModeTUI
)

type Options struct {
	Owner string

	Name string

	Visibility Visibility

	Description string

	AddReadme *bool

	Category config.Category

	Group string

	ProjectName string

	Mode Mode

	WsRoot string

	Workspace *config.Workspace

	Save func(*config.Workspace) error

	GHRunner ghRunner

	URLFor func(owner, name string) string
}

type Result struct {
	Project config.Project
	Name    string
	URL     string
	Cloned  bool
}
