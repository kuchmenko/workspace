package github

import (
	"fmt"
	"os/exec"

	"github.com/kuchmenko/workspace/internal/auth"
)

// ResolveClient returns a GitHub API client based on available auth.
// Priority: ws token → gh CLI → error.
func ResolveClient() (Client, error) {
	// 1. Check for ws token
	token, err := auth.LoadToken()
	if err == nil && token.AccessToken != "" {
		return NewHTTPClient(token.AccessToken), nil
	}

	// 2. Check for gh CLI
	if _, err := exec.LookPath("gh"); err == nil {
		return NewGHClient(), nil
	}

	// 3. No auth available
	return nil, fmt.Errorf("no GitHub authentication found.\n  Run 'ws auth login' to authenticate with GitHub\n  Or install gh CLI: https://cli.github.com")
}
