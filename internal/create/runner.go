package create

import (
	"context"
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// ErrCancelled is returned by Run when the user dismisses the TUI
// without confirming. The cobra layer maps this to a soft exit (no
// error printed, exit 0) since cancellation is a user action, not a
// failure.
var ErrCancelled = errors.New("create canceled by user")

// runTUI launches the model as a tea.Program and returns the captured
// Result when the user confirms. Cancellation (Esc, Ctrl+C) returns
// (nil, ErrCancelled).
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

	prog := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithContext(ctx),
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
