package cli

import (
	"path/filepath"
	"testing"
)

func TestSyncNotificationWorkspacesIncludesOnlyLogicalWorkspace(t *testing.T) {
	root := filepath.Join(t.TempDir(), "logical")
	identities := syncNotificationWorkspaces(root)
	if !identities[cleanAbsolutePath(root)] {
		t.Fatal("logical workspace missing")
	}
	if len(identities) != 1 {
		t.Fatalf("workspace identities = %v", identities)
	}
}
