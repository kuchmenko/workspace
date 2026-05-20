package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kuchmenko/workspace/internal/add"
	"github.com/kuchmenko/workspace/internal/agent"
	"github.com/kuchmenko/workspace/internal/alias"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/create"
	"github.com/kuchmenko/workspace/internal/daemon"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/github"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/kuchmenko/workspace/internal/setup"
	"github.com/kuchmenko/workspace/internal/tui"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var (
	wsRoot string
	ws     *config.Workspace
)

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "ws",
		Short: "Workspace manager — track, sync, and manage development projects",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Name() == "help" || cmd.Name() == "completion" || cmd.Name() == "docs" {
				return nil
			}

			if cmd.Name() == "agent" || cmd.Name() == "ws" {
				return nil
			}
			if cmd.Parent() != nil && cmd.Parent().Name() == "daemon" {
				return nil
			}
			if cmd.Parent() != nil && cmd.Parent().Name() == "auth" {
				return nil
			}

			if cmd.Name() == "setup" {
				var err error
				if wsRoot == "" {
					wsRoot, err = os.Getwd()
					if err != nil {
						return err
					}
				}
				ws, err = config.LoadOrCreate(wsRoot)
				return err
			}

			var err error
			if wsRoot == "" {
				wsRoot, err = config.FindRoot()
				if err != nil {
					return err
				}
			}
			ws, err = config.Load(wsRoot)
			if err != nil {
				return err
			}
			return nil
		},

		RunE: func(cmd *cobra.Command, args []string) error {
			if isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd()) {
				return runExplorerTUI()
			}
			return cmd.Help()
		},
		SilenceUsage: true,
	}

	root.PersistentFlags().StringVar(&wsRoot, "root", "", "workspace root directory (default: auto-detect)")

	root.AddCommand(
		newSyncCmd(),
		newAddCmd(),
		newCreateCmd(),
		newPathCmd(),
		newStatusCmd(),
		newScanCmd(),
		newSetupCmd(),
		newAuthCmd(),
		newDaemonCmd(),
		newAliasCmd(),
		newMigrateCmd(),
		newWorktreeCmd(),
		newBootstrapCmd(),
		newExplorerCmd(),
		newFavoriteCmd(),
		newDocsCmd(),
		newDoctorCmd(),
	)

	return root
}

func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func saveWorkspace() error {
	if err := config.Save(wsRoot, ws); err != nil {
		return fmt.Errorf("saving workspace.toml: %w", err)
	}

	if err := alias.WriteStateFile(ws, wsRoot); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update alias state file: %v\n", err)
	}

	if client, err := daemon.Dial(); err == nil {
		_ = client.Notify(wsRoot, "config_changed")
		client.Close()
	}
	return nil
}

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

	cmd.SetContext(context.Background())

	return cmd
}

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

func projectNameFromPath(p string) string {
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[idx+1:]
	}
	return p
}

func groupOrCategory(p config.Project) string {
	if p.Group != "" {
		return p.Group
	}
	return string(p.Category)
}

func newAliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alias",
		Short: "Manage shell aliases for projects and groups",
		Annotations: map[string]string{
			"capability": "organization",
			"agent:when": "Manage shell aliases (cd shortcuts) for projects and groups via TUI or subcommands",
		},
		RunE: runAliasTUI,
	}
	cmd.AddCommand(
		newAliasListCmd(),
		newAliasAddCmd(),
		newAliasRmCmd(),
		newAliasInitCmd(),
		newAliasInstallCmd(),
	)
	return cmd
}

func runAliasTUI(cmd *cobra.Command, args []string) error {
	m := alias.NewManagerModel(ws, wsRoot)
	p := tui.NewProgram(m, tui.WithAltScreen())
	res, err := p.Run()
	if err != nil {
		return fmt.Errorf("TUI crashed: %w", err)
	}
	final := res.(alias.ManagerModel)
	r := final.GetResult()
	if r.Canceled || !r.Confirmed {
		fmt.Println("Aliases unchanged.")
		return nil
	}
	ws.Aliases = r.Aliases
	if err := saveWorkspace(); err != nil {
		return err
	}
	fmt.Printf("Saved %d aliases.\n", len(ws.Aliases))
	return nil
}

func newAliasListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured aliases",
		Annotations: map[string]string{
			"capability": "organization",
			"agent:when": "List all configured shell aliases with their targets and resolved paths",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(ws.Aliases) == 0 {
				fmt.Println("No aliases defined. Run `ws alias` to create some.")
				return nil
			}
			resolved := alias.ResolveAll(ws, wsRoot)
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ALIAS\tTARGET\tKIND\tPATH")
			for _, r := range resolved {
				kind := r.Kind.String()
				path := r.Path
				if r.Kind == alias.TargetUnknown {
					path = "(broken)"
					kind = "?"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Name, r.Target, kind, path)
			}
			return tw.Flush()
		},
	}
}

func newAliasAddCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "add <alias> <target>",
		Short: "Add an alias for a project or group",
		Annotations: map[string]string{
			"capability": "organization",
			"agent:when": "Create a shell alias that cd's into a project or group directory",
		},
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, target := args[0], args[1]
			if target != alias.RootTarget {
				if _, ok := ws.Projects[target]; !ok {
					if _, ok := ws.Groups[target]; !ok {
						return fmt.Errorf("target %q is not a known project or group (use %q for workspace root)", target, alias.RootTarget)
					}
				}
			}
			if existing, ok := ws.Aliases[name]; ok && !force {
				return fmt.Errorf("alias %q already exists (→ %s); use --force to overwrite", name, existing)
			}
			if path, conflict := alias.ShellConflict(name); conflict && !force {
				return fmt.Errorf("alias %q would shadow existing command at %s; use --force to override", name, path)
			}
			if ws.Aliases == nil {
				ws.Aliases = make(map[string]string)
			}
			ws.Aliases[name] = target
			if err := saveWorkspace(); err != nil {
				return err
			}
			fmt.Printf("Added alias %s → %s\n", name, target)
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "overwrite existing alias or shadow existing command")
	return c
}

func newAliasRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <alias>",
		Short: "Remove an alias",
		Annotations: map[string]string{
			"capability": "organization",
			"agent:when": "Remove a previously defined shell alias",
		},
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if _, ok := ws.Aliases[name]; !ok {
				return fmt.Errorf("alias %q not defined", name)
			}
			delete(ws.Aliases, name)
			if err := saveWorkspace(); err != nil {
				return err
			}
			fmt.Printf("Removed alias %s\n", name)
			return nil
		},
	}
}

func newAliasInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [shell]",
		Short: "Print shell snippet to eval (default: zsh)",
		Annotations: map[string]string{
			"capability": "organization",
			"agent:when": "Output shell init snippet for sourcing aliases (eval in .zshrc)",
		},
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			shell := "zsh"
			if len(args) == 1 {
				shell = args[0]
			}
			if shell != "zsh" {
				return fmt.Errorf("shell %q not supported (only zsh for now)", shell)
			}
			resolved := alias.ResolveAll(ws, wsRoot)
			fmt.Print(alias.RenderZsh(resolved))
			return nil
		},
	}
}

func newAliasInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Add a sourcing line to ~/.zshrc (idempotent)",
		Annotations: map[string]string{
			"capability": "organization",
			"agent:when": "Install alias auto-loading into ~/.zshrc (idempotent, safe to re-run)",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := alias.WriteStateFile(ws, wsRoot); err != nil {
				return err
			}
			added, rc, err := alias.InstallZshrc()
			if err != nil {
				return err
			}
			path, _ := alias.StateFilePath()
			if !added {
				fmt.Printf("Already installed in %s\n", rc)
				fmt.Printf("Aliases sourced from %s\n", path)
				return nil
			}
			fmt.Printf("Installed sourcing block in %s\n", rc)
			fmt.Printf("Aliases will be loaded from %s\n", path)
			fmt.Println("Open a new shell or run: source ~/.zshrc")
			return nil
		},
	}
}

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage GitHub authentication",
		Annotations: map[string]string{
			"capability": "auth",
			"agent:when": "Manage GitHub authentication for repo discovery and API access",
		},
	}

	cmd.AddCommand(
		newAuthLoginCmd(),
		newAuthLogoutCmd(),
		newAuthStatusCmd(),
	)

	return cmd
}

func newAuthLoginCmd() *cobra.Command {
	var usePAT bool

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with GitHub",
		Annotations: map[string]string{
			"capability":   "auth",
			"agent:when":   "Authenticate with GitHub via device flow or personal access token",
			"agent:safety": "Interactive: requires user to complete GitHub device flow or paste a PAT.",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var token github.Token
			var err error

			if usePAT {
				token, err = github.PromptPAT()
			} else {
				token, err = github.DeviceFlow()
			}
			if err != nil {
				return err
			}

			if err := github.SaveToken(token); err != nil {
				return fmt.Errorf("saving token: %w", err)
			}

			path, _ := github.TokenPath()
			fmt.Printf("\n  Authenticated! Token saved to %s\n", path)
			return nil
		},
	}

	cmd.Flags().BoolVar(&usePAT, "pat", false, "use Personal Access Token instead of device flow")
	return cmd
}

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove stored authentication",
		Annotations: map[string]string{
			"capability": "auth",
			"agent:when": "Remove the stored GitHub token",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if !github.HasToken() {
				fmt.Println("  Not authenticated.")
				return nil
			}
			if err := github.DeleteToken(); err != nil {
				return err
			}
			fmt.Println("  Token removed.")
			return nil
		},
	}
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current authentication status",
		Annotations: map[string]string{
			"capability": "auth",
			"agent:when": "Check whether GitHub authentication is configured and valid",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			token, err := github.LoadToken()
			if err != nil {
				fmt.Println("  Not authenticated.")
				fmt.Println("  Run 'ws auth login' to authenticate with GitHub.")
				return nil
			}

			req, err := http.NewRequest(http.MethodGet, "https://api.github.com/user", nil)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+token.AccessToken)
			req.Header.Set("Accept", "application/vnd.github+json")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				fmt.Printf("  Token stored (created %s) but API unreachable\n", token.CreatedAt.Format("2006-01-02"))
				return nil
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				fmt.Printf("  Token stored but invalid (HTTP %d). Run 'ws auth login' to re-authenticate.\n", resp.StatusCode)
				return nil
			}

			var user struct {
				Login string `json:"login"`
			}
			json.NewDecoder(resp.Body).Decode(&user)

			path, _ := github.TokenPath()
			fmt.Printf("  Authenticated as: %s\n", user.Login)
			fmt.Printf("  Token: %s\n", path)
			fmt.Printf("  Scopes: %s\n", token.Scope)
			fmt.Printf("  Created: %s\n", token.CreatedAt.Format("2006-01-02 15:04"))
			return nil
		},
	}
}

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
			"agent:safety": "Performs a write operation against GitHub (creates a repository) and writes to workspace.toml. Requires gh auth login. Holds a `create` sidecar while running so the daemon pauses for the affected workspace.",
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

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the workspace daemon",
		Annotations: map[string]string{
			"capability": "daemon",
			"agent:when": "Control the background daemon that auto-syncs projects across machines",
		},
	}

	cmd.AddCommand(
		newDaemonRunCmd(),
		newDaemonStartCmd(),
		newDaemonStopCmd(),
		newDaemonRestartCmd(),
		newDaemonStatusCmd(),
		newDaemonRegisterCmd(),
		newDaemonUnregisterCmd(),
		newDaemonInstallServiceCmd(),
	)

	return cmd
}

func newDaemonRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "run",
		Short:  "Run daemon in foreground (used by systemd)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return daemon.Run()
		},
	}
}

func newDaemonStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the daemon in background",
		Annotations: map[string]string{
			"capability": "daemon",
			"agent:when": "Start the workspace sync daemon in the background",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if pid, running := daemon.IsRunning(); running {
				return fmt.Errorf("daemon already running (pid %d)", pid)
			}
			_, err := daemon.StartBackground()
			return err
		},
	}
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon",
		Annotations: map[string]string{
			"capability": "daemon",
			"agent:when": "Stop the running workspace sync daemon",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.Dial()
			if err != nil {
				if pid, running := daemon.IsRunning(); running {
					proc, _ := os.FindProcess(pid)
					proc.Signal(os.Interrupt)
					fmt.Printf("  Sent interrupt to pid %d\n", pid)
					return nil
				}
				return fmt.Errorf("daemon not running")
			}
			defer client.Close()
			if err := client.Stop(); err != nil {
				return err
			}
			fmt.Println("  Daemon stopped.")
			return nil
		},
	}
}

func newDaemonRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon",
		Annotations: map[string]string{
			"capability": "daemon",
			"agent:when": "Restart the daemon (stop + start)",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if client, err := daemon.Dial(); err == nil {
				_ = client.Stop()
				client.Close()
			}
			_, err := daemon.StartBackground()
			return err
		},
	}
}

func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		Annotations: map[string]string{
			"capability": "daemon",
			"agent:when": "Check if the daemon is running and which workspaces it watches",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := daemon.Dial()
			if err != nil {
				if pid, running := daemon.IsRunning(); running {
					fmt.Printf("  Daemon running (pid %d) but socket unreachable\n", pid)
				} else {
					fmt.Println("  Daemon not running.")
				}
				return nil
			}
			defer client.Close()

			status, err := client.Status()
			if err != nil {
				return err
			}

			fmt.Printf("  Running (pid %d)\n", status.PID)
			fmt.Printf("  Workspaces: %d\n", len(status.Workspaces))
			for _, w := range status.Workspaces {
				fmt.Printf("    %s (auto_sync=%v)\n", w.Root, w.AutoSync)
			}
			return nil
		},
	}
}

func newDaemonRegisterCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "register [path]",
		Short: "Register a workspace with the daemon",
		Annotations: map[string]string{
			"capability": "daemon",
			"agent:when": "Register a workspace directory so the daemon auto-syncs it",
		},
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := wsRoot
			if len(args) == 1 {
				root = args[0]
			}
			if err := daemon.RegisterWorkspace(root); err != nil {
				return err
			}
			fmt.Printf("  Registered: %s\n", root)
			return nil
		},
	}
}

func newDaemonUnregisterCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unregister [path]",
		Short: "Unregister a workspace from the daemon",
		Annotations: map[string]string{
			"capability": "daemon",
			"agent:when": "Remove a workspace from the daemon's watch list",
		},
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := wsRoot
			if len(args) == 1 {
				root = args[0]
			}
			if err := daemon.UnregisterWorkspace(root); err != nil {
				return err
			}
			fmt.Printf("  Unregistered: %s\n", root)
			return nil
		},
	}
}

func newDaemonInstallServiceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install-service",
		Short: "Install systemd user service for auto-start",
		Annotations: map[string]string{
			"capability": "daemon",
			"agent:when": "Install a systemd user unit so the daemon starts automatically on login",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			unitDir := filepath.Join(home, ".config", "systemd", "user")
			if err := os.MkdirAll(unitDir, 0o755); err != nil {
				return err
			}

			unitContent := `[Unit]
Description=ws workspace manager daemon
After=network.target

[Service]
Type=simple
ExecStart=%h/.local/bin/ws daemon run
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`
			unitPath := filepath.Join(unitDir, "ws-daemon.service")
			if err := os.WriteFile(unitPath, []byte(unitContent), 0o644); err != nil {
				return err
			}
			fmt.Printf("  Installed: %s\n", unitPath)

			exec.Command("systemctl", "--user", "daemon-reload").Run()
			if err := exec.Command("systemctl", "--user", "enable", "--now", "ws-daemon").Run(); err != nil {
				fmt.Println("  Unit installed. Enable manually: systemctl --user enable --now ws-daemon")
				return nil
			}
			fmt.Println("  Service enabled and started.")
			return nil
		},
	}
}

const (
	exitDoctorOK         = 0
	exitDoctorIssues     = 1
	exitDoctorFixApplied = 2
)

func newDoctorCmd() *cobra.Command {
	var (
		fix        bool
		asJSON     bool
		skipRemote bool
	)

	cmd := &cobra.Command{
		Use:   "doctor [project]",
		Short: "Diagnose the workspace — system + per-project health checks",
		Long: `Diagnose the workspace.

Runs system-level checks (daemon, stale sidecars, active conflicts,
config validity) followed by per-project checks (layout, fetch refspec,
remote URL, reachability, default branch, branch upstream, index locks).

Exit codes:
  0  all checks passed
  1  one or more issues found
  2  --fix applied at least one auto-fix

With --fix, every finding that advertises an auto-fix is applied in
batch (no prompts). Fixes that require judgement — resolving conflicts,
clearing index.lock — are never auto-applied; the report prints a hint
and leaves the action to the user.`,
		Annotations: map[string]string{
			"capability":   "observability",
			"agent:when":   "Diagnose workspace health; surface missing refspecs, stale sidecars, conflicts, config issues.",
			"agent:safety": "Read-only unless --fix is set. --fix only applies safe, idempotent mutations (refspec, remote URL, branch upstream, default_branch, stale sidecars).",
		},
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			only := ""
			if len(args) == 1 {
				only = args[0]
				if _, ok := ws.Projects[only]; !ok {
					return fmt.Errorf("unknown project %q", only)
				}
			}

			r := &Runner{
				WsRoot:     wsRoot,
				WS:         ws,
				Only:       only,
				SkipRemote: skipRemote,
			}

			streaming := !asJSON && !fix
			if streaming {
				first := true
				r.OnScope = func(scope string, findings []Finding) {
					WriteScope(os.Stdout, scope, findings, first)
					first = false
				}
			}
			report := r.Run()

			var fixesApplied int
			if fix {
				fixesApplied = ApplyFixes(report)
			}

			switch {
			case asJSON:
				if err := WriteJSON(os.Stdout, report); err != nil {
					return err
				}
			case streaming:
				WriteFooter(os.Stdout, report, FixableCount(report))
			default:
				WriteText(os.Stdout, report)
			}

			os.Exit(exitCodeFor(report, fix, fixesApplied))
			return nil
		},
	}

	cmd.Flags().BoolVar(&fix, "fix", false, "apply all safe auto-fixes in batch (no prompts)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit machine-readable JSON instead of text")
	cmd.Flags().BoolVar(&skipRemote, "skip-remote", false, "skip network-touching checks (remote reachability)")

	return cmd
}

func exitCodeFor(rep *Report, fixRequested bool, fixesApplied int) int {
	if fixRequested && fixesApplied > 0 {
		return exitDoctorFixApplied
	}
	if rep.MaxSeverity() >= Warn {
		return exitDoctorIssues
	}
	return exitDoctorOK
}

func newExplorerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "explorer",
		Aliases: []string{"agent"},
		Short:   "TUI explorer for projects, worktrees, and Claude sessions",
		Annotations: map[string]string{
			"capability":   "explorer",
			"agent:when":   "Browse workspaces, projects, and worktrees, then launch or resume Claude Code sessions",
			"agent:safety": "Interactive TUI. Use subcommands (launch, shell, resume) for non-interactive access.",
		},
		Long: `Launch the interactive TUI explorer over every registered workspace.
The pinned quick-nav header shows up to nine numbered chips (favorites
+ recently-touched) — press 1-9 to launch the matching project. Below
the header, the full project tree scrolls with j/k navigation.

Navigation: j/k to move, Enter to open, h/Esc to go back, q to quit.
1-9 to launch a chip directly. Subcommands provide non-interactive
access to the same actions.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExplorerTUI()
		},
	}
	cmd.AddCommand(
		newExplorerLaunchCmd(),
		newExplorerShellCmd(),
		newExplorerResumeCmd(),
	)
	return cmd
}

func newExplorerLaunchCmd() *cobra.Command {
	var prompt string
	cmd := &cobra.Command{
		Use:   "launch <project-path>",
		Short: "Launch claude in a project directory (non-interactive)",
		Annotations: map[string]string{
			"capability": "agent",
			"agent:when": "Start a new Claude Code session in a specific project directory",
		},
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			stampLaunchActivity(args[0])
			return agent.LaunchClaude(args[0], "", prompt)
		},
	}
	cmd.Flags().StringVarP(&prompt, "prompt", "p", "", "initial prompt for claude")
	return cmd
}

func newExplorerShellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell <path>",
		Short: "Open shell in a directory (non-interactive)",
		Annotations: map[string]string{
			"capability": "agent",
			"agent:when": "Open a new shell in a specific project directory",
		},
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			stampLaunchActivity(args[0])
			return agent.LaunchShell(args[0])
		},
	}
}

func newExplorerResumeCmd() *cobra.Command {
	var prompt string
	cmd := &cobra.Command{
		Use:   "resume <session-id>",
		Short: "Resume a Claude Code session by ID",
		Annotations: map[string]string{
			"capability": "agent",
			"agent:when": "Resume a previously started Claude Code session by its session ID",
		},
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			session := agent.FindSession(sessionID)
			if session == nil {
				return fmt.Errorf("session %s not found", sessionID)
			}
			stampLaunchActivity(session.Cwd)
			return agent.LaunchClaude(session.Cwd, session.ID, prompt)
		},
	}
	cmd.Flags().StringVarP(&prompt, "prompt", "p", "", "additional prompt for the resumed session")
	return cmd
}

func runExplorerTUI() error {
	cwd, _ := os.Getwd()
	workspaces, sessCache, diagnostics := agent.LoadWorkspaces(cwd)
	for _, d := range diagnostics {
		fmt.Fprintf(os.Stderr, "ws agent: %s\n", d)
	}
	if len(workspaces) == 0 {
		return fmt.Errorf("no workspaces found")
	}

	m := agent.NewModel(workspaces, sessCache)
	p := tui.NewProgram(m, tui.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return err
	}

	if final, ok := finalModel.(*agent.Model); ok && final.Launch != nil {
		stampLaunchActivity(final.Launch.Cwd)
		if final.Launch.ShellOnly {
			return agent.LaunchShell(final.Launch.Cwd)
		}
		return agent.LaunchClaude(final.Launch.Cwd, final.Launch.ResumeID, final.Launch.Prompt)
	}
	return nil
}

func stampLaunchActivity(cwd string) {
	if err := agent.StampLaunchFromPath(cwd); err != nil {
		fmt.Fprintf(os.Stderr, "ws agent: stamp activity: %v\n", err)
	}
}

func newFavoriteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "favorite",
		Short: "Pin projects to the Favorites section of `ws agent`",
		Long: `Manage the project favorites shown at the top of ` + "`" + `ws agent` + "`" + `.

Favorites are stored in workspace.toml and sync across machines via the
reconciler. The same toggle is available in the TUI as the f hotkey on
any project row.`,
		Annotations: map[string]string{
			"capability": "organization",
			"agent:when": "Pin / unpin projects shown in the Favorites section of `ws agent`",
		},
	}
	cmd.AddCommand(
		newFavoriteAddCmd(),
		newFavoriteRmCmd(),
		newFavoriteListCmd(),
	)
	return cmd
}

func newFavoriteAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <project | @group>",
		Short: "Mark a project or group as favorite",
		Args:  cobra.ExactArgs(1),
		Annotations: map[string]string{
			"capability": "organization",
			"agent:when": "Pin a project or group to the quick-nav chips of `ws explorer`",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return setFavorite(args[0], true)
		},
	}
}

func newFavoriteRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <project | @group>",
		Short: "Unmark a favorite project or group",
		Args:  cobra.ExactArgs(1),
		Annotations: map[string]string{
			"capability": "organization",
			"agent:when": "Unpin a project or group from the quick-nav chips of `ws explorer`",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return setFavorite(args[0], false)
		},
	}
}

func setFavorite(arg string, fav bool) error {
	if len(arg) > 1 && arg[0] == '@' {
		return setGroupFavoriteCLI(arg[1:], fav)
	}
	return setProjectFavorite(arg, fav)
}

func setGroupFavoriteCLI(name string, fav bool) error {
	if _, ok := ws.Groups[name]; !ok {
		if ws.Groups == nil {
			ws.Groups = map[string]config.Group{}
		}
		ws.Groups[name] = config.Group{}
	}
	if !ws.SetGroupFavorite(name, fav) {
		if fav {
			fmt.Printf("@%s is already a favorite.\n", name)
		} else {
			fmt.Printf("@%s is not a favorite.\n", name)
		}
		return nil
	}
	if err := saveWorkspace(); err != nil {
		return err
	}
	if fav {
		fmt.Printf("Added @%s to favorites.\n", name)
	} else {
		fmt.Printf("Removed @%s from favorites.\n", name)
	}
	return nil
}

func newFavoriteListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List favorite projects",
		Annotations: map[string]string{
			"capability": "organization",
			"agent:when": "Print favorited projects with their category and group",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var projNames, groupNames []string
			for n, p := range ws.Projects {
				if p.Favorite {
					projNames = append(projNames, n)
				}
			}
			for n, g := range ws.Groups {
				if g.Favorite {
					groupNames = append(groupNames, n)
				}
			}
			if len(projNames)+len(groupNames) == 0 {
				fmt.Println("No favorites. Use `ws favorite add <project | @group>` to pin one.")
				return nil
			}
			sort.Strings(projNames)
			sort.Strings(groupNames)
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tKIND\tCATEGORY\tGROUP")
			for _, n := range groupNames {
				fmt.Fprintf(tw, "@%s\tgroup\t-\t-\n", n)
			}
			for _, n := range projNames {
				p := ws.Projects[n]
				group := p.Group
				if group == "" {
					group = "-"
				}
				fmt.Fprintf(tw, "%s\tproject\t%s\t%s\n", n, p.Category, group)
			}
			return tw.Flush()
		},
	}
}

func setProjectFavorite(name string, fav bool) error {
	p, ok := ws.Projects[name]
	if !ok {
		return fmt.Errorf("unknown project %q (see `ws status`)", name)
	}
	if !p.SetFavorite(fav) {
		if fav {
			fmt.Printf("%s is already a favorite.\n", name)
		} else {
			fmt.Printf("%s is not a favorite.\n", name)
		}
		return nil
	}
	ws.Projects[name] = p
	if err := saveWorkspace(); err != nil {
		return err
	}
	if fav {
		fmt.Printf("Added %s to favorites.\n", name)
	} else {
		fmt.Printf("Removed %s from favorites.\n", name)
	}
	return nil
}

const (
	pathExitOK           = 0
	pathExitMissingDir   = 1
	pathExitUnknownProj  = 2
	pathExitUsage        = 64
	suggestionListCutoff = 5
)

func newPathCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "path [project]",
		Short: "Print absolute path to the workspace root or a project",
		Long: `Resolve a project name to its absolute filesystem path.

With no argument, prints the workspace root. With one argument, prints
the joined absolute path of the named project.

Output is pure path on stdout (\n-terminated); errors go to stderr.

Designed for shell substitution, e.g.:

  cd "$(ws path workspace)"
  code "$(ws path myapp)"

Exit codes:
  0   success
  1   outside any workspace OR project registered but not cloned
  2   project name not present in workspace.toml
  64  usage error (more than one argument)`,
		Annotations: map[string]string{
			"capability": "observability",
			"agent:when": "Resolve a project name to its absolute filesystem path. With no argument, prints the workspace root. Designed for shell substitution: cd \"$(ws path foo)\".",
		},

		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				fmt.Fprintf(cmd.ErrOrStderr(), "ws path: too many arguments (got %d, want 0 or 1)\nusage: ws path [project]\n", len(args))
				osExit(pathExitUsage)
			}
			return nil
		},
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 || ws == nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			names := make([]string, 0, len(ws.Projects))
			for name := range ws.Projects {
				names = append(names, name)
			}
			sort.Strings(names)
			return names, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			code := runPath(cmd.OutOrStdout(), cmd.ErrOrStderr(), wsRoot, ws, args)
			if code != pathExitOK {
				osExit(code)
			}
			return nil
		},
	}
	return cmd
}

var osExit = os.Exit

func runPath(stdout, stderr io.Writer, wsRoot string, ws *config.Workspace, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, wsRoot)
		return pathExitOK
	}
	name := args[0]
	proj, ok := ws.Projects[name]
	if !ok {
		writeUnknownProject(stderr, name, ws.Projects)
		return pathExitUnknownProj
	}
	abs := filepath.Join(wsRoot, proj.Path)
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(stderr, "ws path: not cloned: %q (path: %s)\nhint: ws bootstrap %s\n", name, proj.Path, name)
		return pathExitMissingDir
	}
	fmt.Fprintln(stdout, abs)
	return pathExitOK
}

func writeUnknownProject(w io.Writer, name string, projects map[string]config.Project) {
	fmt.Fprintf(w, "ws path: unknown project %q\n", name)
	if len(projects) == 0 || len(projects) >= suggestionListCutoff {
		return
	}
	names := make([]string, 0, len(projects))
	for n := range projects {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintln(w, "\nregistered projects:")
	for _, n := range names {
		fmt.Fprintf(w, "  %s\n", n)
	}
}

func newScanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scan",
		Short: "Find git repos not registered in workspace.toml",
		Annotations: map[string]string{
			"capability": "project",
			"agent:when": "Discover git repos under standard directories that are not yet tracked in workspace.toml",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			scanDirs := []string{"personal", "work", "playground", "researches", "tools"}
			var found int

			knownPaths := make(map[string]bool)
			for _, proj := range ws.Projects {
				knownPaths[proj.Path] = true
			}

			for _, dir := range scanDirs {
				absDir := filepath.Join(wsRoot, dir)
				if _, err := os.Stat(absDir); os.IsNotExist(err) {
					continue
				}

				err := scanDir(absDir, wsRoot, dir, knownPaths, &found)
				if err != nil {
					fmt.Printf("  warn  scanning %s: %v\n", dir, err)
				}
			}

			if found == 0 {
				fmt.Println("No unregistered repos found.")
			} else {
				fmt.Printf("\n%d unregistered repo(s) found. Use 'ws add <url>' to register.\n", found)
			}
			return nil
		},
	}
}

func scanDir(absDir, root, category string, knownPaths map[string]bool, found *int) error {
	_ = category
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if shouldSkipScanEntry(entry) {
			continue
		}
		entryPath := filepath.Join(absDir, entry.Name())
		if git.IsRepo(entryPath) {
			reportIfUnknownRepo(entryPath, root, knownPaths, found)
			continue
		}
		scanGroupDir(entryPath, root, knownPaths, found)
	}
	return nil
}

func scanGroupDir(groupDir, root string, knownPaths map[string]bool, found *int) {
	entries, err := os.ReadDir(groupDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if shouldSkipScanEntry(entry) {
			continue
		}
		entryPath := filepath.Join(groupDir, entry.Name())
		if git.IsRepo(entryPath) {
			reportIfUnknownRepo(entryPath, root, knownPaths, found)
		}
	}
}

func shouldSkipScanEntry(entry os.DirEntry) bool {
	name := entry.Name()
	if !entry.IsDir() || strings.HasPrefix(name, ".") {
		return true
	}
	return strings.HasSuffix(name, ".bare") || strings.Contains(name, "-wt-")
}

func reportIfUnknownRepo(repoPath, root string, knownPaths map[string]bool, found *int) {
	relPath, _ := filepath.Rel(root, repoPath)
	if knownPaths[relPath] {
		return
	}
	remote, _ := git.RemoteURL(repoPath)
	fmt.Printf("  found  %s (remote: %s)\n", relPath, remote)
	*found++
}

func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Interactive setup — select repos from GitHub and organize into groups",
		Annotations: map[string]string{
			"capability":   "project",
			"agent:when":   "First-time workspace setup: interactively select GitHub repos and organize them into groups",
			"agent:safety": "Interactive TUI — requires user interaction. Writes workspace.toml.",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			m := setup.NewModel()
			p := tui.NewProgram(m, tui.WithAltScreen())

			result, err := p.Run()
			if err != nil {
				return fmt.Errorf("TUI crashed: %w", err)
			}

			final := result.(setup.Model)
			r := final.GetResult()

			if r.Err != nil {
				return fmt.Errorf("setup failed: %w", r.Err)
			}

			if r.Canceled {
				fmt.Println("Setup canceled by user.")
				return nil
			}

			if !r.Confirmed {
				fmt.Println("Setup exited without confirmation.")
				return nil
			}

			for _, group := range r.Groups {
				ws.Groups[group.Name] = config.Group{
					Description: "",
				}

				for _, repo := range group.Repos {
					cat := config.CategoryWork
					if repo.Owner == r.Username {
						cat = config.CategoryPersonal
					}

					ws.Projects[repo.Name] = config.Project{
						Remote:   repo.SSHURL,
						Path:     group.Name + "/" + repo.Name,
						Status:   config.StatusActive,
						Category: cat,
						Group:    group.Name,
					}
				}
			}

			if err := saveWorkspace(); err != nil {
				return err
			}

			total := 0
			for _, g := range r.Groups {
				total += len(g.Repos)
			}

			fmt.Printf("\nWorkspace configured: %d groups, %d projects\n", len(r.Groups), total)
			fmt.Printf("Run 'ws sync' to clone all repos.\n")
			return nil
		},
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show all projects with their current state",
		Annotations: map[string]string{
			"capability": "observability",
			"agent:when": "Get an overview of all projects: branch, last commit, layout (plain/worktree/missing)",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(ws.Projects) == 0 {
				fmt.Println("No projects registered. Use 'ws add <url>' to add one.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PROJECT\tGROUP\tSTATUS\tBRANCH\tLAST COMMIT\tLAYOUT")

			names := make([]string, 0, len(ws.Projects))
			for name := range ws.Projects {
				names = append(names, name)
			}
			sort.Strings(names)

			for _, name := range names {
				proj := ws.Projects[name]
				absPath := filepath.Join(wsRoot, proj.Path)

				branch := "-"
				lastCommit := "-"
				layoutInfo := "missing"

				if info, err := os.Stat(absPath); err == nil && info.IsDir() {
					layoutInfo = "plain"
					if git.IsRepo(absPath) {
						if b, err := git.CurrentBranch(absPath); err == nil {
							branch = b
						}
						if t, err := git.LastCommitTime(absPath); err == nil {
							lastCommit = humanizeTime(t)
						}
					}
				}
				if _, err := os.Stat(layout.BarePath(absPath)); err == nil {
					n := countExtraWorktrees(absPath)
					if n > 0 {
						layoutInfo = fmt.Sprintf("worktree+%d", n)
					} else {
						layoutInfo = "worktree"
					}
				}

				groupDisplay := proj.Group
				if groupDisplay == "" {
					groupDisplay = string(proj.Category)
				}

				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					name, groupDisplay, proj.Status, branch, lastCommit, layoutInfo)
			}

			return w.Flush()
		},
	}
}

func countExtraWorktrees(mainPath string) int {
	parent := filepath.Dir(mainPath)
	base := filepath.Base(mainPath) + "-wt-"
	entries, err := os.ReadDir(parent)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), base) {
			n++
		}
	}
	return n
}

func humanizeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		days := int(d.Hours() / 24)
		return fmt.Sprintf("%dd ago", days)
	default:
		return t.Format("2006-01-02")
	}
}
