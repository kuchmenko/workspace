package git

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func EnsureMirrorRemote(repoPath, name, remoteURL string) error {
	if name == "" || name == "origin" {
		return fmt.Errorf("mirror remote name %q is reserved", name)
	}
	if err := SetRemoteURLFor(repoPath, name, remoteURL); err != nil {
		return err
	}
	if err := setConfig(repoPath, "remote."+name+".pushurl", remoteURL); err != nil {
		return err
	}
	return setConfig(repoPath, "remote."+name+".skipFetchAll", "true")
}

func MirrorRemoteOK(repoPath, name, remoteURL string) bool {
	got, err := RemoteURLFor(repoPath, name)
	if err != nil || got != remoteURL {
		return false
	}
	if !RemoteBindingExact(repoPath, name, remoteURL) {
		return false
	}
	cmd := exec.Command("git", "-C", repoPath, "config", "--get", "remote."+name+".skipFetchAll")
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func PushMirror(repoPath, name string) error {
	return PushMirrorContext(context.Background(), repoPath, name)
}

func PushMirrorContext(ctx context.Context, repoPath, name string) error {
	remoteURL, err := ConfiguredRemoteURL(repoPath, name)
	if err != nil {
		return err
	}
	return PushMirrorURLContext(ctx, repoPath, remoteURL)
}

func PushMirrorURLContext(ctx context.Context, repoPath, remoteURL string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "for-each-ref", "--format=%(refname)", "refs/remotes/origin")
	out, err := cmd.Output()
	if err != nil {
		return commandError(ctx, "git for-each-ref in "+repoPath, string(out), err)
	}
	refspecs := []string{"refs/tags/*:refs/tags/*"}
	for _, ref := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		ref = strings.TrimSpace(ref)
		if ref == "" || ref == "refs/remotes/origin/HEAD" {
			continue
		}
		branch := strings.TrimPrefix(ref, "refs/remotes/origin/")
		refspecs = append(refspecs, ref+":refs/heads/"+branch)
	}
	args := append([]string{"-C", repoPath, "push", remoteURL}, refspecs...)
	pushOut, err := remoteCommand(ctx, args...).CombinedOutput()
	if err != nil {
		return commandError(ctx, fmt.Sprintf("git push %s in %s", RedactRemote(remoteURL), repoPath), RedactDiagnostic(string(pushOut), remoteURL), err)
	}
	return nil
}
