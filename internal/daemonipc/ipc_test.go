package daemonipc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWakeDeliversWorkspaceID(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), "registry.db")
	server, err := Listen(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	received := make(chan string, 1)
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, func(workspaceID string) { received <- workspaceID }) }()

	wakeContext, stopWake := context.WithTimeout(context.Background(), time.Second)
	defer stopWake()
	if err = Wake(wakeContext, registryPath, "workspace-1"); err != nil {
		t.Fatal(err)
	}
	select {
	case workspaceID := <-received:
		if workspaceID != "workspace-1" {
			t.Fatalf("workspace ID = %q", workspaceID)
		}
	case <-time.After(time.Second):
		t.Fatal("wake was not delivered")
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWakeWithoutWorkspaceIDRequestsAllWorkspaces(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), "registry.db")
	server, err := Listen(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	received := make(chan string, 1)
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, func(workspaceID string) { received <- workspaceID }) }()

	if err = Wake(ctx, registryPath, ""); err != nil {
		t.Fatal(err)
	}
	if workspaceID := <-received; workspaceID != "" {
		t.Fatalf("workspace ID = %q, want all workspaces", workspaceID)
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestListenRejectsSecondDaemon(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), "registry.db")
	server, err := Listen(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	if _, err = Listen(registryPath); err == nil {
		t.Fatal("second daemon listener succeeded")
	}
}

func TestSocketPathStaysShortForLongRegistryPath(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), strings.Repeat("long-directory-", 10), "registry.db")
	path := SocketPath(registryPath)
	if len(path) >= 100 {
		t.Fatalf("socket path has %d bytes: %s", len(path), path)
	}
	wantDirectory := filepath.Join("/tmp", fmt.Sprintf("ws-%d", os.Getuid()))
	if filepath.Dir(path) != wantDirectory {
		t.Fatalf("socket directory = %q, want %q", filepath.Dir(path), wantDirectory)
	}
}
