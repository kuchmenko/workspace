package registry

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOriginBaselinesAreMachineLocalAndReplaceable(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	if err = store.SaveOriginBaselines(ctx, "workspace", map[string]string{"app": "old", "api": "api"}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveOriginBaselines(ctx, "workspace", map[string]string{"app": "new"}); err != nil {
		t.Fatal(err)
	}
	baselines, err := store.OriginBaselines(ctx, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if len(baselines) != 1 || baselines["app"] != "new" {
		t.Fatalf("baselines = %#v", baselines)
	}
}
