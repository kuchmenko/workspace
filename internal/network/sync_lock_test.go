package network

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSyncLockSerializesSameWorkspacePeerAndHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	first, err := acquireSyncLock(context.Background(), path, "workspace", "peer")
	if err != nil {
		t.Fatal(err)
	}
	defer first.release()

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	if _, err = acquireSyncLock(ctx, path, "workspace", "peer"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contended lock error = %v", err)
	}

	first.release()
	second, err := acquireSyncLock(context.Background(), path, "workspace", "peer")
	if err != nil {
		t.Fatal(err)
	}
	second.release()
}
