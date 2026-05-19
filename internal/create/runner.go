package create

import (
	"context"
	"errors"
	"fmt"
	"github.com/kuchmenko/workspace/internal/tui"
)

var ErrCancelled = errors.New("create canceled by user")

func runTUI(ctx context.Context, opts Options) (*Result, error) {
	model := NewCreateModel(CreateModelOptions{
		WsRoot:      opts.WsRoot,
		Workspace:   opts.Workspace,
		Save:        resolveSaveFn(opts),
		GHRunner:    opts.GHRunner,
		Owner:       opts.Owner,
		Name:        opts.Name,
		Visibility:  opts.Visibility,
		Description: opts.Description,
		Category:    opts.Category,
		Group:       opts.Group,
		ProjectName: opts.ProjectName,
		URLFor:      opts.URLFor,
	})

	prog := tui.NewProgram(
		model,
		tui.WithAltScreen(),
		tui.WithContext(ctx),
	)
	finalModel, err := prog.Run()
	if err != nil {
		return nil, fmt.Errorf("create TUI: %w", err)
	}
	final, ok := finalModel.(CreateModel)
	if !ok {
		return nil, fmt.Errorf("create TUI: unexpected final model type %T", finalModel)
	}
	if final.canceled {
		return nil, ErrCancelled
	}
	if final.err != nil {
		return nil, final.err
	}
	if final.result == nil {
		return nil, errors.New("create TUI exited with no result")
	}
	return final.result, nil
}
