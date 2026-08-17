package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/kuchmenko/workspace/internal/registry"
	"github.com/spf13/cobra"
)

func newWorkspaceConflictsCmd() *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "conflicts <workspace>",
		Short: "List unresolved workspace registry conflicts",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			store, err := registry.OpenDefault()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			conflicts, err := store.Conflicts(command.Context(), args[0])
			if err != nil {
				return err
			}
			if jsonOutput {
				encoder := json.NewEncoder(command.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(conflicts)
			}
			return writeWorkspaceConflicts(command.OutOrStdout(), conflicts)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	return command
}

func writeWorkspaceConflicts(writer io.Writer, conflicts []registry.Conflict) error {
	for _, conflict := range conflicts {
		if _, err := fmt.Fprintf(writer, "%s\tbase=%s\tleft=%s\tright=%s\n", terminalText(conflict.Path), displayJSON(conflict.Base), displayJSON(conflict.Left), displayJSON(conflict.Right)); err != nil {
			return err
		}
	}
	return nil
}

func newWorkspaceResolveCmd() *cobra.Command {
	var take, value string
	command := &cobra.Command{
		Use:   "resolve <workspace> <path>",
		Short: "Resolve one workspace registry conflict",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, args []string) error {
			selected, err := workspaceResolutionValue(command, args[0], args[1], take, value)
			if err != nil {
				return err
			}
			store, err := registry.OpenDefault()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			workspace, err := store.Resolve(command.Context(), args[0], args[1], selected)
			if err != nil {
				return err
			}
			fmt.Fprintf(command.OutOrStdout(), "workspace=%s head=%s\n", workspace.Name, workspace.Head)
			return nil
		},
	}
	command.Flags().StringVar(&take, "take", "", "conflict value: base, left, or right")
	command.Flags().StringVar(&value, "value", "", "replacement JSON value")
	return command
}

func workspaceResolutionValue(command *cobra.Command, workspace, path, take, value string) (json.RawMessage, error) {
	if (take == "") == (value == "") {
		return nil, errors.New("set exactly one of --take or --value")
	}
	if value != "" {
		selected := json.RawMessage(value)
		if !json.Valid(selected) {
			return nil, errors.New("--value must be valid JSON")
		}
		return selected, nil
	}
	store, err := registry.OpenDefault()
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	conflicts, err := store.Conflicts(command.Context(), workspace)
	if err != nil {
		return nil, err
	}
	return selectedConflictValue(conflicts, path, take)
}

func selectedConflictValue(conflicts []registry.Conflict, path, take string) (json.RawMessage, error) {
	for _, conflict := range conflicts {
		if conflict.Path != path {
			continue
		}
		switch take {
		case "base":
			return conflict.Base, nil
		case "left":
			return conflict.Left, nil
		case "right":
			return conflict.Right, nil
		default:
			return nil, errors.New("--take must be base, left, or right")
		}
	}
	return nil, fmt.Errorf("workspace conflict %q not found", path)
}

func displayJSON(value json.RawMessage) string {
	if len(value) == 0 {
		return "<deleted>"
	}
	return string(value)
}
