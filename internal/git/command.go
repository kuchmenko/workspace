package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func remoteCommand(ctx context.Context, args ...string) *exec.Cmd {
	sshCommand := os.Getenv("GIT_SSH_COMMAND")
	if sshCommand == "" {
		sshCommand = "ssh"
	}
	sshCommand += " -oBatchMode=yes -oStrictHostKeyChecking=yes"
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=never",
		"GIT_SSH_COMMAND="+sshCommand,
	)
	return cmd
}

func commandError(ctx context.Context, operation, output string, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%s: %w", operation, ctx.Err())
	}
	output = strings.TrimSpace(output)
	if output == "" {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s: %s", operation, output)
}
