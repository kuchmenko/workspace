package layout

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlugifyBranch(t *testing.T) {
	cases := map[string]string{
		"feat/foo":           "feat-foo",
		"feat/auth-refactor": "feat-auth-refactor",
		"main":               "main",
		"-trim-me-":          "trim-me",
		"a/b/c":              "a-b-c",
	}
	for in, want := range cases {
		if got := SlugifyBranch(in); got != want {
			t.Errorf("SlugifyBranch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWorktreeDirName_FlattensSlashes(t *testing.T) {
	got := WorktreeDirName("myapp", "linux", "feat/auth-refactor")
	want := "myapp-wt-linux-feat-auth-refactor"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWorktreePathForBranch_NoCollision(t *testing.T) {
	parent := t.TempDir()
	mainWT := filepath.Join(parent, "myapp")
	if err := os.MkdirAll(mainWT, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got := WorktreePathForBranch(mainWT, "linux", "feat/foo")
	want := filepath.Join(parent, "myapp-wt-linux-feat-foo")
	if got != want {
		t.Errorf("no collision case: got %q, want %q", got, want)
	}
}

func TestWorktreePathForBranch_CollisionGetsSha8Suffix(t *testing.T) {
	parent := t.TempDir()
	mainWT := filepath.Join(parent, "myapp")
	if err := os.MkdirAll(mainWT, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Pre-create the would-be slug to force a collision.
	if err := os.MkdirAll(filepath.Join(parent, "myapp-wt-linux-feat-foo-bar"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	branch := "feat/foo/bar" // slug = "feat-foo-bar", same as the squatter
	got := WorktreePathForBranch(mainWT, "linux", branch)
	sum := sha1.Sum([]byte(branch))
	wantSuffix := hex.EncodeToString(sum[:4])
	want := filepath.Join(parent, "myapp-wt-linux-feat-foo-bar-"+wantSuffix)
	if got != want {
		t.Errorf("collision case: got %q, want %q", got, want)
	}
}

func TestWorktreePathForBranch_DeterministicAcrossMachines(t *testing.T) {
	// Two machines independently call WorktreePathForBranch on the same branch
	// against an empty parent — they must agree on the un-suffixed path.
	// Then both pre-create a colliding entry; they must agree on the suffix.
	branchA := "feat/auth"
	for _, machine := range []string{"linux", "archlinux"} {
		// Empty parent: no collision.
		parent1 := t.TempDir()
		mainWT1 := filepath.Join(parent1, "myapp")
		if err := os.MkdirAll(mainWT1, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		_ = WorktreePathForBranch(mainWT1, machine, branchA)
		// Forced collision: pre-create the slug.
		parent2 := t.TempDir()
		mainWT2 := filepath.Join(parent2, "myapp")
		if err := os.MkdirAll(mainWT2, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(parent2, "myapp-wt-"+machine+"-feat-auth"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		got := WorktreePathForBranch(mainWT2, machine, branchA)
		// Suffix must be the SHA1[:8] of the branch name regardless of machine.
		sum := sha1.Sum([]byte(branchA))
		wantSuffix := hex.EncodeToString(sum[:4])
		if !strings.HasSuffix(got, "-"+wantSuffix) {
			t.Errorf("machine=%s: expected suffix -%s, got %q", machine, wantSuffix, got)
		}
	}
}

func TestWorktreePathForBranch_DistinctBranchesGetDistinctSuffixes(t *testing.T) {
	parent := t.TempDir()
	mainWT := filepath.Join(parent, "myapp")
	if err := os.MkdirAll(mainWT, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Pre-occupy the slug so both branches collide.
	slug := "myapp-wt-linux-feat-foo-bar"
	if err := os.MkdirAll(filepath.Join(parent, slug), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	a := WorktreePathForBranch(mainWT, "linux", "feat/foo-bar")
	b := WorktreePathForBranch(mainWT, "linux", "feat/foo/bar")
	if a == b {
		t.Errorf("distinct branches with the same slug must get distinct paths: %q == %q", a, b)
	}
	if !strings.HasPrefix(filepath.Base(a), slug+"-") {
		t.Errorf("branch a should be suffixed: %q", a)
	}
	if !strings.HasPrefix(filepath.Base(b), slug+"-") {
		t.Errorf("branch b should be suffixed: %q", b)
	}
}

func TestBarePath(t *testing.T) {
	if got := BarePath("/dev/personal/myapp"); got != "/dev/personal/myapp.bare" {
		t.Errorf("got %q", got)
	}
}
