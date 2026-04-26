package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kuchmenko/workspace/internal/add"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	var (
		category string
		group    string
		name     string
		noClone  bool
		noTUI    bool
		tui      bool
	)

	cmd := &cobra.Command{
		Use:   "add [remote-url...]",
		Short: "Register and clone new projects",
		Long: `Register one or more git repositories in workspace.toml and clone them
into the bare+worktree layout used by every other ws command.

Three input modes:

  ws add <url>            register and clone a single URL
  ws add <url> <url> ...  register and clone several URLs (sequential)
  ws add -                read URLs from stdin, one per line
  ws add                  open the interactive TUI with disk / clipboard / GitHub suggestions

Headless invocations (any with positional URLs, or stdin '-', or a non-TTY
context) call clone.CloneIntoLayout — the same path 'ws bootstrap' uses —
so new projects land directly in <path>.bare + <path> form. No follow-up
'ws migrate' is required.`,
		Annotations: map[string]string{
			"capability":   "project",
			"agent:when":   "Register a new git repository in workspace.toml and clone it locally as bare+worktree",
			"agent:safety": "Creates new directories (.bare + worktree) and updates workspace.toml. Use --no-clone to register without cloning. Holds an `add` sidecar while running so the daemon pauses for the affected workspace.",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if tui && noTUI {
				return errors.New("--tui and --no-tui are mutually exclusive")
			}

			urls, err := collectURLs(args)
			if err != nil {
				return err
			}

			if name != "" && len(urls) > 1 {
				return errors.New("--name is only valid with a single URL")
			}

			mode := add.ModeAuto
			switch {
			case tui:
				mode = add.ModeTUI
			case noTUI:
				mode = add.ModeHeadless
			case len(urls) == 0:
				// No URLs and no explicit mode flag — fall through to
				// add.Run's auto handling. add.Run will still error
				// with ErrTUINotImplemented in Phase 2 because the
				// TUI ships in Phase 3, but the dispatch shape is in
				// place for a flag-flip when Phase 3 lands.
			default:
				if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
					mode = add.ModeHeadless
				}
			}

			cat := config.Category(category)
			if cat == "" {
				cat = config.CategoryPersonal
			}

			res, err := add.Run(cmd.Context(), add.Options{
				URLs:      urls,
				Category:  cat,
				Group:     group,
				Name:      name,
				NoClone:   noClone,
				Mode:      mode,
				WsRoot:    wsRoot,
				Workspace: ws,
				Save:      func(*config.Workspace) error { return saveWorkspace() },
			})
			if err != nil {
				return err
			}

			printResult(res)

			// Non-zero exit only if something actually failed; per-URL
			// failures in Errors are user-visible above. ErrAlreadyRegistered
			// is in Skipped, not Errors, so it doesn't trip exit.
			if len(res.Errors) > 0 {
				return fmt.Errorf("%d of %d URL(s) failed", len(res.Errors), len(urls))
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&category, "category", "c", "personal", "project category: personal or work")
	cmd.Flags().StringVarP(&group, "group", "g", "", "group/directory for the project (e.g. limitless, personal/tools)")
	cmd.Flags().StringVarP(&name, "name", "n", "", "project name (default: derived from URL; only valid with a single URL)")
	cmd.Flags().BoolVar(&noClone, "no-clone", false, "register without cloning")
	cmd.Flags().BoolVar(&tui, "tui", false, "force interactive TUI (default when no URLs given on a TTY)")
	cmd.Flags().BoolVar(&noTUI, "no-tui", false, "force headless mode; error if no URLs are provided")

	// cmd.Context() is a no-op default; wire to a real context for now.
	// The CLI is invoked synchronously from main, so a Background ctx
	// covers ordinary cases. A future signal-aware context can replace
	// this without touching add.Run.
	cmd.SetContext(context.Background())

	return cmd
}

// collectURLs assembles the URL list from positional args. The dash
// sentinel "-" means "read from stdin, one URL per line, ignoring
// blank lines and shell-style # comments". Mixing "-" with other args
// is allowed: positional URLs come first in the resulting slice, then
// the stdin batch.
func collectURLs(args []string) ([]string, error) {
	var urls []string
	for _, a := range args {
		if a == "-" {
			batch, err := readURLsFromStdin()
			if err != nil {
				return nil, err
			}
			urls = append(urls, batch...)
			continue
		}
		urls = append(urls, a)
	}
	return urls, nil
}

// readURLsFromStdin reads non-blank, non-comment lines from stdin.
// Comments use '#'. Returns nil + error only on read failure; an
// empty stdin returns (nil, nil) and the caller decides whether to
// treat that as a no-op or an error.
func readURLsFromStdin() ([]string, error) {
	var out []string
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	return out, nil
}

// printResult renders one human-readable line per Added project, plus
// one line per Skipped/Errored URL. Format mirrors the legacy single-URL
// output ("clone X → Y" / "added X") so existing eyeballs/parsers see
// familiar shapes.
func printResult(res *add.Result) {
	for _, p := range res.Added {
		fmt.Printf("  added  %s (group: %s, %s)\n", projectNameFromPath(p.Path), groupOrCategory(p), p.Status)
	}
	for _, s := range res.Skipped {
		fmt.Printf("  skip   %s — %s\n", s.URL, s.Reason)
	}
	for _, e := range res.Errors {
		fmt.Printf("  error  %s\n", e)
	}
	if total := len(res.Added) + len(res.Skipped) + len(res.Errors); total > 1 {
		fmt.Printf("\n%d added, %d skipped, %d errored\n", len(res.Added), len(res.Skipped), len(res.Errors))
	}
}

// projectNameFromPath strips the directory from a workspace-relative
// project path. The Project struct has Path = "<group>/<name>" or
// "<category>/<name>"; we render the trailing component.
func projectNameFromPath(p string) string {
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[idx+1:]
	}
	return p
}

// groupOrCategory returns the group when set, else the category, for
// the success-line summary. Matches legacy `ws add` output behavior.
func groupOrCategory(p config.Project) string {
	if p.Group != "" {
		return p.Group
	}
	return string(p.Category)
}
