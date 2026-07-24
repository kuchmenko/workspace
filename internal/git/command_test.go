package git

import (
	"context"
	"strings"
	"testing"
)

func TestRemoteCommandIsNonInteractiveAndRequiresKnownSSHHosts(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "")
	cmd := remoteCommand(context.Background(), "ls-remote", "git@example.com:owner/project.git")
	env := strings.Join(cmd.Env, "\n")
	for _, setting := range []string{
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=never",
		"GIT_SSH_COMMAND=ssh -oBatchMode=yes -oStrictHostKeyChecking=yes",
	} {
		if !strings.Contains(env, setting) {
			t.Errorf("command environment missing %q", setting)
		}
	}
}

func TestRemoteCommandPreservesConfiguredSSHCommand(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "ssh -i /tmp/key")
	cmd := remoteCommand(context.Background(), "ls-remote", "git@example.com:owner/project.git")
	env := strings.Join(cmd.Env, "\n")
	want := "GIT_SSH_COMMAND=ssh -i /tmp/key -oBatchMode=yes -oStrictHostKeyChecking=yes"
	if !strings.Contains(env, want) {
		t.Errorf("command environment missing %q", want)
	}
}
