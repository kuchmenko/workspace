package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/kuchmenko/workspace/internal/registry"
)

type promptAction struct {
	key, label string
	handler    func() (done bool, err error)
}

func runPromptLoop(actions []promptAction, skipKeys ...string) (bool, error) {
	for {
		printPromptMenu(actions, skipKeys)
		choice := strings.TrimSpace(readLine())
		if isSkipKey(choice, skipKeys) {
			return false, nil
		}
		done, err := dispatchPromptChoice(choice, actions)
		if err != nil || done {
			return done, err
		}
	}
}

func printPromptMenu(actions []promptAction, skipKeys []string) {
	fmt.Println("Options:")
	for _, action := range actions {
		fmt.Printf("  (%s) %s\n", action.key, action.label)
	}
	for _, key := range skipKeys {
		if key == "" {
			continue
		}
		fmt.Printf("  (%s) skip — leave for later\n", key)
	}
	fmt.Print("> ")
}

func isSkipKey(choice string, skipKeys []string) bool {
	for _, key := range skipKeys {
		if choice == key {
			return true
		}
	}
	return false
}

func dispatchPromptChoice(choice string, actions []promptAction) (bool, error) {
	for _, action := range actions {
		if action.key == choice {
			return action.handler()
		}
	}
	return false, nil
}

func shellAndConfirm(dir string) (bool, error) {
	if err := openShell(dir); err != nil {
		return false, err
	}
	fmt.Println("returned from shell. Mark conflict resolved? (y/N)")
	if strings.EqualFold(strings.TrimSpace(readLine()), "y") {
		return true, nil
	}
	return false, nil
}

func resolveProjectConflict(c conflict.Conflict) (bool, error) {
	wtPath, err := findWorktreePath(c.Workspace, c.Project, c.Branch)
	if err != nil {
		fmt.Printf("warning: could not locate worktree: %v\n", err)
	}
	return runPromptLoop([]promptAction{
		{"l", "git log local..remote (remote-only commits)",
			func() (bool, error) { runDivergenceLog(wtPath, c.Branch+"..@{u}"); return false, nil }},
		{"r", "git log remote..local (local-only commits)",
			func() (bool, error) { runDivergenceLog(wtPath, "@{u}.."+c.Branch); return false, nil }},
		{"o", "open shell in worktree — fix manually",
			func() (bool, error) { return openShellAtWorktree(wtPath) }},
	}, "k", "")
}

func resolveOriginDivergence(c conflict.Conflict) (bool, error) {
	return runPromptLoop([]promptAction{
		{"l", "use local checkout origin",
			func() (bool, error) { return true, resolveOriginDivergenceTo(c, true) }},
		{"s", "use shared registry origin",
			func() (bool, error) { return true, resolveOriginDivergenceTo(c, false) }},
	}, "k", "")
}

func resolveOriginDivergenceTo(c conflict.Conflict, useLocal bool) error {
	ctx := context.Background()
	store, err := registry.OpenDefault()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	workspace, err := store.LoadByRoot(ctx, c.Workspace)
	if err != nil {
		return err
	}
	project, exists := workspace.State.Projects[c.Project]
	if !exists {
		return fmt.Errorf("project %s not in workspace registry", c.Project)
	}
	mainPath, err := layout.ProjectPath(c.Workspace, project.Path)
	if err != nil {
		return err
	}
	repository := layout.BarePath(mainPath)
	if !git.IsRepo(repository) {
		repository = mainPath
	}
	local, err := git.ConfiguredRemoteURL(repository, "origin")
	if err != nil {
		return fmt.Errorf("read origin in %s: %w", repository, err)
	}
	chosen := project.Remote
	if useLocal {
		chosen = local
	}
	baselines, err := store.OriginBaselines(ctx, workspace.WorkspaceID)
	if err != nil {
		return err
	}
	previous, hadPrevious := baselines[c.Project]
	baselines[c.Project] = chosen
	if err = store.SaveOriginBaselines(ctx, workspace.WorkspaceID, baselines); err != nil {
		return err
	}
	if useLocal {
		project.Remote = chosen
		workspace.State.Projects[c.Project] = project
		_, err = store.Update(ctx, workspace.Name, workspace.Revision, workspace.State)
	} else {
		err = git.SetRemoteURL(repository, chosen)
	}
	if err == nil {
		return nil
	}
	if hadPrevious {
		baselines[c.Project] = previous
	} else {
		delete(baselines, c.Project)
	}
	return errors.Join(err, store.SaveOriginBaselines(ctx, workspace.WorkspaceID, baselines))
}

func openShellAtWorktree(wtPath string) (bool, error) {
	if wtPath == "" {
		fmt.Println("no worktree path; cannot open shell")
		return false, nil
	}
	return shellAndConfirm(wtPath)
}

func runDivergenceLog(wtPath, gitRange string) {
	if wtPath == "" {
		return
	}
	_ = runInTerm(wtPath, "git", "log", "--oneline", gitRange)
}

func findWorktreePath(workspace, project, branch string) (string, error) {
	if ws == nil {
		return "", fmt.Errorf("workspace not loaded")
	}
	proj, ok := ws.Projects[project]
	if !ok {
		return "", fmt.Errorf("project %s not in workspace registry", project)
	}
	mainPath := workspace + string(os.PathSeparator) + proj.Path
	if branch == "" {
		return mainPath, nil
	}
	barePath := layout.BarePath(mainPath)
	if wtPath := locateWorktreeForBranch(barePath, branch); wtPath != "" {
		return wtPath, nil
	}
	return mainPath, nil
}

func openShell(dir string) error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runInTerm(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func readLine() string {
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}
