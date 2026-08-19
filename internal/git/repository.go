package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var ErrRemoteRefNotFound = errors.New("remote ref not found")

const standardFetchRefspec = "+refs/heads/*:refs/remotes/origin/*"

func IsBare(path string) bool {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--is-bare-repository")
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
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
		if strings.Contains(string(out), "couldn't find remote ref") {
			return fmt.Errorf("%w: %s", ErrRemoteRefNotFound, strings.TrimSpace(RedactDiagnostic(string(out), source)))
		}
		return fmt.Errorf("git fetch %s %s in %s: %s", RedactRemote(source), refspec, repoPath, strings.TrimSpace(RedactDiagnostic(string(out), source)))
	}
	return nil
}

func SetFetchRefspec(repoPath string) error {
	return setConfig(repoPath, "remote.origin.fetch", standardFetchRefspec)
}

func HasFetchRefspec(repoPath string) bool {
	cmd := exec.Command("git", "-C", repoPath, "config", "--get-all", "remote.origin.fetch")
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

func HasBranch(repoPath, branch string) bool {
	return exec.Command("git", "-C", repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run() == nil
}

func HasRemoteBranch(repoPath, remote, branch string) bool {
	return exec.Command("git", "-C", repoPath, "show-ref", "--verify", "--quiet", "refs/remotes/"+remote+"/"+branch).Run() == nil
}

func FetchRemoteBranch(repoPath, remote, branch string) (string, error) {
	trackingRef := "refs/remotes/" + remote + "/" + branch
	if err := FetchRefspec(repoPath, remote, "+refs/heads/"+branch+":"+trackingRef); err != nil {
		return "", err
	}
	out, err := exec.Command("git", "-C", repoPath, "rev-parse", "--verify", trackingRef+"^{commit}").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve %s in %s: %s", trackingRef, repoPath, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func DeleteLocalBranch(repoPath, branch, expectedOID string) error {
	ref := "refs/heads/" + branch
	out, err := exec.Command("git", "-C", repoPath, "update-ref", "-d", ref, expectedOID).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git update-ref delete %s at %s in %s: %s", ref, expectedOID, repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func DeleteRemoteBranch(repoPath, remote, branch, expectedOID string) error {
	ref := "refs/heads/" + branch
	out, err := remoteCommand(context.Background(), "-C", repoPath, "push", "--force-with-lease="+ref+":"+expectedOID, remote, ":"+ref).CombinedOutput()
	if err != nil {
		return commandError(context.Background(), fmt.Sprintf("git push %s leased delete %s in %s", RedactRemote(remote), branch, repoPath), RedactDiagnostic(string(out), remote), err)
	}
	return nil
}

func FastForwardURLBranchContext(ctx context.Context, repoPath, remoteURL, branch string) error {
	if err := FetchURLBranchContext(ctx, repoPath, remoteURL, branch); err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, "git", "-C", repoPath, "merge", "--ff-only", "refs/remotes/origin/"+branch).CombinedOutput()
	if err != nil {
		return commandError(ctx, "git merge --ff-only origin/"+branch+" in "+repoPath, RedactDiagnostic(string(out), remoteURL), err)
	}
	return nil
}

func RebaseURLBranchContext(ctx context.Context, repoPath, remoteURL, branch string) error {
	if err := FetchURLBranchContext(ctx, repoPath, remoteURL, branch); err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, "git", "-C", repoPath, "rebase", "refs/remotes/origin/"+branch).CombinedOutput()
	if err != nil {
		return commandError(ctx, "git rebase origin/"+branch+" in "+repoPath, RedactDiagnostic(string(out), remoteURL), err)
	}
	return nil
}

func IsRepo(path string) bool {
	return exec.Command("git", "-C", path, "rev-parse", "--git-dir").Run() == nil
}

func CurrentBranch(repoPath string) (string, error) {
	out, err := exec.Command("git", "-C", repoPath, "branch", "--show-current").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func Branches(repoPath string) ([]string, error) {
	out, err := exec.Command("git", "-C", repoPath, "branch", "--format=%(refname:short)").Output()
	if err != nil {
		return nil, err
	}
	var branches []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			branches = append(branches, line)
		}
	}
	return branches, nil
}

func LastCommitTime(repoPath string) (time.Time, error) {
	out, err := exec.Command("git", "-C", repoPath, "log", "-1", "--format=%cI").Output()
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
}

func LastCommitMessage(repoPath string) (string, error) {
	out, err := exec.Command("git", "-C", repoPath, "log", "-1", "--format=%s").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func Add(repoPath, file string) error {
	out, err := exec.Command("git", "-C", repoPath, "add", file).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git add in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func Commit(repoPath, message string) error {
	out, err := exec.Command("git", "-C", repoPath, "commit", "-m", message).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git commit in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}

func PushURLBranchContext(ctx context.Context, repoPath, remoteURL, branch string) error {
	refspec := "refs/heads/" + branch + ":refs/heads/" + branch
	out, err := remoteCommand(ctx, "-C", repoPath, "push", remoteURL, refspec).CombinedOutput()
	if err != nil {
		return commandError(ctx, "git push "+RedactRemote(remoteURL)+" "+refspec+" in "+repoPath, RedactDiagnostic(string(out), remoteURL), err)
	}
	return nil
}

func PushBranch(repoPath, branch string) error {
	return PushBranchContext(context.Background(), repoPath, branch)
}

func PushBranchContext(ctx context.Context, repoPath, branch string) error {
	out, err := remoteCommand(ctx, "-C", repoPath, "push", "--set-upstream", "origin", branch).CombinedOutput()
	if err != nil {
		return commandError(ctx, fmt.Sprintf("git push %s in %s", branch, repoPath), RedactDiagnostic(string(out)), err)
	}
	return nil
}

func Fetch(repoPath string) error {
	return FetchContext(context.Background(), repoPath)
}

func FetchContext(ctx context.Context, repoPath string) error {
	return FetchRemoteContext(ctx, repoPath, "origin")
}

func FetchRemoteContext(ctx context.Context, repoPath, remote string) error {
	out, err := remoteCommand(ctx, "-C", repoPath, "fetch", "--prune", remote).CombinedOutput()
	if err != nil {
		return commandError(ctx, "git fetch "+RedactRemote(remote)+" in "+repoPath, RedactDiagnostic(string(out), remote), err)
	}
	return fetchNewTags(ctx, repoPath, remote)
}

func FetchURLContext(ctx context.Context, repoPath, remoteURL string) error {
	refspec := "+refs/heads/*:refs/remotes/origin/*"
	out, err := remoteCommand(ctx, "-C", repoPath, "fetch", "--prune", remoteURL, refspec).CombinedOutput()
	if err != nil {
		return commandError(ctx, "git fetch "+RedactRemote(remoteURL)+" in "+repoPath, RedactDiagnostic(string(out), remoteURL), err)
	}
	return fetchNewTags(ctx, repoPath, remoteURL)
}

func fetchNewTags(ctx context.Context, repoPath, remote string) error {
	localOutput, err := exec.CommandContext(ctx, "git", "-C", repoPath, "for-each-ref", "--format=%(refname)", "refs/tags").Output()
	if err != nil {
		return commandError(ctx, "list local tags in "+repoPath, "", err)
	}
	local := make(map[string]bool)
	for _, ref := range strings.Fields(string(localOutput)) {
		local[ref] = true
	}
	remoteOutput, err := remoteCommand(ctx, "-C", repoPath, "ls-remote", "--tags", "--refs", remote).CombinedOutput()
	if err != nil {
		return commandError(ctx, "list tags from "+RedactRemote(remote), RedactDiagnostic(string(remoteOutput), remote), err)
	}
	var missing []string
	for _, line := range strings.Split(strings.TrimSpace(string(remoteOutput)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.HasPrefix(fields[1], "refs/tags/") && !local[fields[1]] {
			missing = append(missing, fields[1])
		}
	}
	for start := 0; start < len(missing); start += 128 {
		end := min(start+128, len(missing))
		arguments := []string{"-C", repoPath, "fetch", "--no-tags", remote}
		for _, ref := range missing[start:end] {
			arguments = append(arguments, ref+":"+ref)
		}
		out, fetchErr := remoteCommand(ctx, arguments...).CombinedOutput()
		if fetchErr != nil {
			return commandError(ctx, "fetch tags from "+RedactRemote(remote)+" in "+repoPath, RedactDiagnostic(string(out), remote), fetchErr)
		}
	}
	return nil
}

func FetchURLBranchContext(ctx context.Context, repoPath, remoteURL, branch string) error {
	refspec := "+refs/heads/" + branch + ":refs/remotes/origin/" + branch
	out, err := remoteCommand(ctx, "-C", repoPath, "fetch", remoteURL, refspec).CombinedOutput()
	if err != nil {
		return commandError(ctx, "git fetch "+RedactRemote(remoteURL)+" "+refspec+" in "+repoPath, RedactDiagnostic(string(out), remoteURL), err)
	}
	return nil
}

func RevParse(repoPath, ref string) string {
	out, err := exec.Command("git", "-C", repoPath, "rev-parse", ref).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func AheadBehind(repoPath, branch string) (ahead, behind int, hasUpstream bool) {
	upstream := branch + "@{u}"
	return aheadBehindRefs(repoPath, branch, upstream)
}

func AheadBehindRemote(repoPath, branch, remote string) (ahead, behind int, hasRemote bool) {
	return aheadBehindRefs(repoPath, branch, "refs/remotes/"+remote+"/"+branch)
}

func aheadBehindRefs(repoPath, branch, upstream string) (ahead, behind int, exists bool) {
	if RevParse(repoPath, upstream) == "" {
		return 0, 0, false
	}
	out, err := exec.Command("git", "-C", repoPath, "rev-list", "--left-right", "--count", upstream+"..."+branch).Output()
	if err != nil {
		return 0, 0, false
	}
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) != 2 {
		return 0, 0, false
	}
	fmt.Sscanf(parts[0], "%d", &behind)
	fmt.Sscanf(parts[1], "%d", &ahead)
	return ahead, behind, true
}

func IsDirty(repoPath string) bool {
	dirty, err := WorktreeModified(repoPath)
	return err != nil || dirty
}

func WorktreeModified(repoPath string) (bool, error) {
	out, err := exec.Command("git", "-C", repoPath, "status", "--porcelain").Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func HasIndexLock(repoPath string) bool {
	gitDir := RevParse(repoPath, "--git-dir")
	if gitDir == "" {
		return false
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repoPath, gitDir)
	}
	_, err := os.Stat(filepath.Join(gitDir, "index.lock"))
	return err == nil
}

func HasUpstream(repoPath, branch string) bool {
	return exec.Command("git", "-C", repoPath, "rev-parse", "--abbrev-ref", branch+"@{upstream}").Run() == nil
}

func SetBranchUpstream(repoPath, branch, remote string) error {
	if branch == "" || remote == "" {
		return fmt.Errorf("SetBranchUpstream: empty branch or remote")
	}
	if err := setConfig(repoPath, "branch."+branch+".remote", remote); err != nil {
		return err
	}
	return setConfig(repoPath, "branch."+branch+".merge", "refs/heads/"+branch)
}

func setConfig(repoPath, key, value string) error {
	out, err := exec.Command("git", "-C", repoPath, "config", key, value).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git config %s=%s in %s: %s", key, RedactRemote(value), repoPath, strings.TrimSpace(RedactDiagnostic(string(out), value)))
	}
	return nil
}

func HasStash(repoPath string) bool {
	return StashCount(repoPath) > 0
}

func StashCount(repoPath string) int {
	out, err := exec.Command("git", "-C", repoPath, "stash", "list").Output()
	if err != nil {
		return 0
	}
	stash := strings.TrimSpace(string(out))
	if stash == "" {
		return 0
	}
	return strings.Count(stash, "\n") + 1
}

func SymbolicRef(repoPath, ref string) string {
	out, err := exec.Command("git", "-C", repoPath, "symbolic-ref", "--short", ref).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
