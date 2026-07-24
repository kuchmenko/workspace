package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"codeberg.org/kuchmenko/workspace/internal/config"
	"codeberg.org/kuchmenko/workspace/internal/create"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

func newCreateCmd() *cobra.Command {
	var (
		owner       string
		name        string
		visibility  string
		isPublic    bool
		description string
		category    string
		group       string
		projectName string
		noTUI       bool
		tui         bool
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new GitHub repo and register it as a workspace project",
		Long: `Create a new GitHub repository (in your account or any accessible org)
via the gh CLI, register it in workspace.toml, and clone it into the
bare+worktree layout used by every other ws command.

Without flags on a TTY, opens an interactive form:

  - Owner       — pick from your personal account or orgs you belong to
  - Name        — repo name (1-100 chars, GitHub-compatible)
  - Visibility  — private (default) or public
  - Description — optional one-liner
  - Category    — personal (default) or work
  - Group       — optional project group/dir for workspace.toml

The new repo is always created with --add-readme so it has a default
branch + first commit, which lets clone succeed without
ErrNeedsBootstrap.

Headless mode (any combination of --owner + --name + --no-tui or both
required flags on a non-TTY):

  ws create --owner kuchmenko --name foo
  ws create --owner my-org --name bar --public --description "..."

Requires gh authentication: run 'gh auth login' first.`,
		Annotations: map[string]string{
			"capability":   "project",
			"agent:when":   "Create a new GitHub repository, register it in workspace.toml, and clone it locally as bare+worktree",
			"agent:safety": "Performs a write operation against GitHub (creates a repository) and writes to workspace.toml. Requires gh auth login. Holds a `create` sidecar while running.",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if tui && noTUI {
				return errors.New("--tui and --no-tui are mutually exclusive")
			}

			vis, err := resolveVisibility(visibility, isPublic)
			if err != nil {
				return err
			}

			cat := config.Category(category)
			if cat == "" {
				cat = config.CategoryPersonal
			}

			mode := create.ModeAuto
			switch {
			case tui:
				mode = create.ModeTUI
			case noTUI:
				mode = create.ModeHeadless
			default:

				if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
					mode = create.ModeHeadless
				}
			}

			res, err := create.Run(cmd.Context(), create.Options{
				Owner:       owner,
				Name:        name,
				Visibility:  vis,
				Description: description,
				Category:    cat,
				Group:       group,
				ProjectName: projectName,
				Mode:        mode,
				WsRoot:      wsRoot,
				Workspace:   ws,
				Save:        func(*config.Workspace) error { return saveWorkspace() },
			})
			if errors.Is(err, create.ErrCancelled) {
				return nil
			}
			if err != nil {
				return err
			}

			fmt.Printf("  created  %s\n", res.URL)
			fmt.Printf("  added    %s (group: %s, %s)\n",
				res.Name,
				groupOrCategory(res.Project),
				res.Project.Status,
			)
			return nil
		},
	}

	cmd.Flags().StringVarP(&owner, "owner", "o", "", "GitHub owner (user or org); required for headless mode")
	cmd.Flags().StringVarP(&name, "name", "n", "", "new repo name (required for headless mode)")
	cmd.Flags().StringVarP(&visibility, "visibility", "v", "", "repo visibility: private|public (default private)")
	cmd.Flags().BoolVar(&isPublic, "public", false, "shorthand for --visibility=public")
	cmd.Flags().StringVarP(&description, "description", "d", "", "optional repo description")
	cmd.Flags().StringVarP(&category, "category", "c", "personal", "project category: personal or work")
	cmd.Flags().StringVarP(&group, "group", "g", "", "project group/dir for workspace.toml (default: owner login for work, category for personal)")
	cmd.Flags().StringVar(&projectName, "project-name", "", "override the workspace.toml project key (default: repo name)")
	cmd.Flags().BoolVar(&tui, "tui", false, "force interactive TUI")
	cmd.Flags().BoolVar(&noTUI, "no-tui", false, "force headless mode; --owner and --name become required")

	cmd.SetContext(context.Background())
	return cmd
}

func resolveVisibility(visibility string, isPublic bool) (create.Visibility, error) {
	if isPublic {
		return create.VisibilityPublic, nil
	}
	switch visibility {
	case "":
		return create.VisibilityPrivate, nil
	case "private":
		return create.VisibilityPrivate, nil
	case "public":
		return create.VisibilityPublic, nil
	default:
		return "", fmt.Errorf("invalid --visibility %q (want private|public)", visibility)
	}
}
