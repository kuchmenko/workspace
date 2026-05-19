package add

import (
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/github"
)

type Mode int

const (
	ModeAuto Mode = iota

	ModeHeadless

	ModeTUI

	ModeEmbedded
)

type Options struct {
	URLs []string

	Category config.Category

	Group string

	Name string

	NoClone bool

	Mode Mode

	WsRoot string

	Workspace *config.Workspace

	Save func(*config.Workspace) error

	GhProvider github.Provider
}

type Result struct {
	Added []config.Project

	Skipped []SkipReason

	Errors []error
}

type SkipReason struct {
	URL    string
	Reason string
}
