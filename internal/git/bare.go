package git

import (
	"fmt"
	"os/exec"
	"strings"
)

func CloneBare(remote, dest string) error {
	cmd := exec.Command("git", "clone", "--bare", remote, dest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone --bare %s: %s", remote, strings.TrimSpace(string(out)))
	}
	return nil
}

func CloneBareLocal(srcRepoPath, destBarePath string) error {
	cmd := exec.Command("git", "clone", "--bare", "--no-local", srcRepoPath, destBarePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone --bare --no-local %s: %s", srcRepoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func IsBare(path string) bool {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--is-bare-repository")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

func SetRemoteURL(repoPath, url string) error {
	cmd := exec.Command("git", "-C", repoPath, "remote", "set-url", "origin", url)
	if err := cmd.Run(); err == nil {
		return nil
	}
	cmd = exec.Command("git", "-C", repoPath, "remote", "add", "origin", url)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("set remote in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func SetRemoteHead(repoPath, branch string) error {
	cmd := exec.Command("git", "-C", repoPath, "remote", "set-head", "origin", branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("set remote head %s in %s: %s", branch, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func FetchRefspec(repoPath, source, refspec string) error {
	cmd := exec.Command("git", "-C", repoPath, "fetch", source, refspec)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git fetch %s %s in %s: %s", source, refspec, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

const standardFetchRefspec = "+refs/heads/*:refs/remotes/origin/*"

func SetFetchRefspec(repoPath string) error {
	return setConfig(repoPath, "remote.origin.fetch", standardFetchRefspec)
}

func HasFetchRefspec(repoPath string) bool {
	cmd := exec.Command("git", "-C", repoPath, "config", "--get-all", "remote.origin.fetch")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

func HasBranch(repoPath, branch string) bool {
	cmd := exec.Command("git", "-C", repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}

func HasRemoteBranch(repoPath, remote, branch string) bool {
	cmd := exec.Command("git", "-C", repoPath, "show-ref", "--verify", "--quiet", "refs/remotes/"+remote+"/"+branch)
	return cmd.Run() == nil
}
