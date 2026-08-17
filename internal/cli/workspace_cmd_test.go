package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/device"
	peernetwork "github.com/kuchmenko/workspace/internal/network"
	"github.com/kuchmenko/workspace/internal/registry"
	"github.com/spf13/cobra"
)

func TestWorkspaceCommandsCreateAndList(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(directory, "state"))
	workspace := t.TempDir()
	cmd := newWorkspaceCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"create", workspace, "--name", "personal"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("workspace create: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "workspace=personal") || !strings.Contains(got, "root="+workspace) {
		t.Fatalf("workspace create output = %q", got)
	}
	if _, err := os.Stat(filepath.Join(workspace, "workspace.toml")); !os.IsNotExist(err) {
		t.Fatalf("workspace create wrote workspace.toml: %v", err)
	}

	out.Reset()
	cmd = newWorkspaceCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("workspace list: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "personal\t"+workspace+"\n") {
		t.Fatalf("workspace list output = %q", got)
	}

	newRoot := t.TempDir()
	out.Reset()
	cmd = newWorkspaceCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"set-root", "personal", newRoot})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("workspace set-root: %v", err)
	}
	if got := out.String(); got != "workspace=personal root="+newRoot+"\n" {
		t.Fatalf("workspace set-root output = %q", got)
	}
	out.Reset()
	cmd = newWorkspaceCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("workspace list after set-root: %v", err)
	}
	if got := out.String(); got != "personal\t"+newRoot+"\n" {
		t.Fatalf("workspace list after set-root output = %q", got)
	}
}

func TestWorkspaceShareAndAccessCommands(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(directory, "state"))
	store, err := registry.OpenDefault()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err = store.EnsureNetwork(ctx, "arch"); err != nil {
		t.Fatal(err)
	}
	peer, err := device.Load(filepath.Join(directory, "peer.key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddNetworkDevice(ctx, "asahi", peer.PublicKey(), registry.NetworkMember); err != nil {
		t.Fatal(err)
	}
	state := &config.Workspace{Meta: config.Meta{Version: 1}, Groups: map[string]config.Group{}, Projects: map[string]config.Project{}, Aliases: map[string]string{}}
	if _, err = store.Create(ctx, "personal", t.TempDir(), state); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	command := newWorkspaceCmd()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"share", "personal", "--with", "all", "--role", "writer"})
	if err = command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "workspace=personal mode=all epoch=1") {
		t.Fatalf("share output = %q", output.String())
	}
	output.Reset()
	command = newWorkspaceCmd()
	command.SetOut(&output)
	command.SetArgs([]string{"access", "personal"})
	if err = command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "all\twriter") || !strings.Contains(output.String(), "arch\tadmin") {
		t.Fatalf("access output = %q", output.String())
	}
}

func TestWorkspaceNetworkCommandSelectionAndOutput(t *testing.T) {
	available := []peernetwork.AvailableWorkspace{
		{WorkspaceSummary: registry.WorkspaceSummary{WorkspaceID: "alpha-id", Name: "personal"}, DeviceName: "arch"},
		{WorkspaceSummary: registry.WorkspaceSummary{WorkspaceID: "beta-id", Name: "work"}, DeviceName: "asahi"},
	}
	selected, err := selectAvailableWorkspace(available, "alpha")
	if err != nil || selected.Name != "personal" {
		t.Fatalf("selected=%#v error=%v", selected, err)
	}
	if _, err = selectAvailableWorkspace(available, "missing"); err == nil {
		t.Fatal("missing workspace was selected")
	}
	results := []peernetwork.SyncResult{
		{Workspace: "personal", Device: "arch", Status: "pulled"},
		{Workspace: "personal", Device: "lxc", Status: "unavailable"},
	}
	command := &cobra.Command{}
	var output bytes.Buffer
	command.SetOut(&output)
	if err = writeWorkspaceSyncResults(command, results, false); err != nil {
		t.Fatal(err)
	}
	if output.String() != "personal\tarch\tpulled\npersonal\tlxc\tunavailable\n" {
		t.Fatalf("sync output = %q", output.String())
	}
	output.Reset()
	if err = writeWorkspaceSyncResults(command, results, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"status": "pulled"`) || !strings.Contains(output.String(), `"status": "unavailable"`) {
		t.Fatalf("sync JSON = %q", output.String())
	}
	results = []peernetwork.SyncResult{{Workspace: "personal\x1b[2J", Device: "arch\u009b2J", Status: "pulled\nforged"}}
	output.Reset()
	if err = writeWorkspaceSyncResults(command, results, false); err != nil {
		t.Fatal(err)
	}
	if output.String() != "personal\\x1B[2J\tarch\\x9B2J\tpulled\\x0Aforged\n" {
		t.Fatalf("escaped sync output = %q", output.String())
	}
	output.Reset()
	if err = writeWorkspaceSyncResults(command, results, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"workspace": "personal\u001b[2J"`) {
		t.Fatalf("sync JSON changed = %q", output.String())
	}
}

func TestAvailableWorkspaceOutputEscapesPeerProvidedNames(t *testing.T) {
	available := []peernetwork.AvailableWorkspace{{
		WorkspaceSummary: registry.WorkspaceSummary{Name: "shared\x1b[2J", Role: "writer"},
		DeviceName:       "peer\nforged\x1b]0;owned\a",
		Endpoint:         "127.0.0.1:17337",
	}}
	var output bytes.Buffer
	if err := writeAvailableWorkspaces(&output, available); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(output.String(), "\x1b\a") || strings.Contains(output.String(), "peer\nforged") {
		t.Fatalf("availability output contains peer control characters: %q", output.String())
	}
}

func TestAttachedWorkspaceOutputEscapesPeerProvidedNames(t *testing.T) {
	workspace := registry.Workspace{Name: "shared\x1b[2J", Root: "/tmp/shared"}
	var output bytes.Buffer
	if err := writeAttachedWorkspace(&output, workspace, "peer\nforged\x1b]0;owned\a"); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(output.String(), "\x1b\a") || strings.Contains(output.String(), "peer\nforged") {
		t.Fatalf("attach output contains peer control characters: %q", output.String())
	}
}

func TestWorkspaceAccessOutputEscapesPeerDeviceNames(t *testing.T) {
	policy := registry.AccessPolicy{
		Mode:   registry.AccessSelected,
		Roles:  map[string]string{"role-id": registry.WorkspaceWriter},
		Denied: []string{"denied-id"},
	}
	names := map[string]string{
		"role-id":   "writer\nforged\x1b]0;owned\a",
		"denied-id": "denied\x1b[2J",
	}
	var output bytes.Buffer
	if err := writeWorkspaceAccess(&output, policy, names); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(output.String(), "\x1b\a") || strings.Contains(output.String(), "writer\nforged") {
		t.Fatalf("access output contains peer control characters: %q", output.String())
	}
}

func TestWorkspaceConflictOutputEscapesReplicatedPath(t *testing.T) {
	conflicts := []registry.Conflict{{Path: "/aliases/peer\nforged\x1b]0;owned\a", Base: json.RawMessage(`"base"`)}}
	var output bytes.Buffer
	if err := writeWorkspaceConflicts(&output, conflicts); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(output.String(), "\x1b\a") || strings.Contains(output.String(), "peer\nforged") {
		t.Fatalf("conflict output contains replicated control characters: %q", output.String())
	}
}

func TestSynchronizeWorkspacePeersContextPullsRemoteRevision(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(directory, "state"))
	ctx := context.Background()
	left, err := registry.OpenDefault()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = left.Close() }()
	rightPath := filepath.Join(directory, "right", "registry.db")
	right, err := registry.Open(rightPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = right.Close() }()
	leftIdentityPath, err := device.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	leftIdentity, err := device.Load(leftIdentityPath)
	if err != nil {
		t.Fatal(err)
	}
	rightIdentity, err := device.Load(filepath.Join(filepath.Dir(rightPath), "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = left.EnsureNetwork(ctx, "arch"); err != nil {
		t.Fatal(err)
	}
	leftNetwork, err := left.AddNetworkDevice(ctx, "asahi", rightIdentity.PublicKey(), registry.NetworkMember)
	if err != nil {
		t.Fatal(err)
	}
	networkBundle, err := left.ExportNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = right.ImportNetwork(ctx, networkBundle, leftIdentity.ID()); err != nil {
		t.Fatal(err)
	}
	state := &config.Workspace{Meta: config.Meta{Version: 1}, Groups: map[string]config.Group{}, Projects: map[string]config.Project{}, Aliases: map[string]string{}}
	leftRoot, rightRoot := t.TempDir(), t.TempDir()
	created, err := left.Create(ctx, "shared", leftRoot, state)
	if err != nil {
		t.Fatal(err)
	}
	policy := registry.AccessPolicy{Mode: registry.AccessAll, DefaultRole: registry.WorkspaceWriter, Roles: map[string]string{leftIdentity.ID(): registry.WorkspaceAdmin}}
	if _, err = left.SetAccess(ctx, "shared", policy); err != nil {
		t.Fatal(err)
	}
	initial, err := left.ExportFor(ctx, "shared", rightIdentity.ID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = right.AttachFrom(ctx, "shared", rightRoot, initial, leftIdentity.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err = right.Mutate(ctx, rightRoot, func(workspace *config.Workspace) error {
		workspace.Aliases["remote"] = "project"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	serveCtx, stop := context.WithCancel(ctx)
	defer stop()
	ready := make(chan string, 1)
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- peernetwork.Serve(serveCtx, peernetwork.ServeOptions{
			Store: right, Identity: rightIdentity, Name: "asahi", ListenAddress: "127.0.0.1:0", DisableDiscovery: true,
			Ready: func(endpoint string) { ready <- endpoint },
		})
	}()
	endpoint := <-ready
	var rightDevice registry.DeviceRecord
	for _, record := range leftNetwork.Devices {
		if record.ID == rightIdentity.ID() {
			rightDevice = record
		}
	}
	results, failures, err := synchronizeWorkspacePeersContext(ctx, left, leftIdentity, "arch", []registry.Workspace{created}, []peernetwork.PeerEndpoint{{Device: rightDevice, Endpoint: endpoint}})
	if err != nil || len(failures) != 0 || len(results) != 1 || results[0].Status != "pulled" {
		t.Fatalf("results=%#v failures=%v error=%v", results, failures, err)
	}
	pulled, err := left.LoadByRoot(ctx, leftRoot)
	if err != nil {
		t.Fatal(err)
	}
	if pulled.State.Aliases["remote"] != "project" {
		t.Fatalf("aliases = %#v", pulled.State.Aliases)
	}
	stop()
	select {
	case err = <-serveErrors:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("peer server did not stop")
	}
	results, failures, err = synchronizeWorkspacePeersContext(ctx, left, leftIdentity, "arch", []registry.Workspace{pulled}, []peernetwork.PeerEndpoint{{Device: rightDevice, Endpoint: endpoint}})
	if err != nil || len(results) != 1 || results[0].Status != "unavailable" || len(failures) != 1 {
		t.Fatalf("offline results=%#v failures=%v error=%v", results, failures, err)
	}
}

func TestWorkspaceConflictCommandsListAndResolve(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(directory, "state"))
	ctx := context.Background()
	left, err := registry.OpenDefault()
	if err != nil {
		t.Fatal(err)
	}
	leftRoot, rightRoot := t.TempDir(), t.TempDir()
	state := &config.Workspace{Meta: config.Meta{Version: 1}, Groups: map[string]config.Group{}, Projects: map[string]config.Project{}, Aliases: map[string]string{"editor": "vim"}}
	if _, err = left.Create(ctx, "personal", leftRoot, state); err != nil {
		t.Fatal(err)
	}
	initial, err := left.Export(ctx, "personal")
	if err != nil {
		t.Fatal(err)
	}
	right, err := registry.Open(filepath.Join(directory, "right", "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = right.Attach(ctx, "personal", rightRoot, initial); err != nil {
		t.Fatal(err)
	}
	if _, err = left.Mutate(ctx, leftRoot, func(workspace *config.Workspace) error {
		workspace.Aliases["editor"] = "helix"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = right.Mutate(ctx, rightRoot, func(workspace *config.Workspace) error {
		workspace.Aliases["editor"] = "nano"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rightBundle, err := right.Export(ctx, "personal")
	if err != nil {
		t.Fatal(err)
	}
	if _, conflicts, integrateErr := left.Integrate(ctx, "personal", rightBundle); integrateErr != nil || len(conflicts) != 1 {
		t.Fatalf("conflicts=%#v error=%v", conflicts, integrateErr)
	}
	if err = right.Close(); err != nil {
		t.Fatal(err)
	}
	if err = left.Close(); err != nil {
		t.Fatal(err)
	}

	command := newWorkspaceCmd()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"conflicts", "personal"})
	if err = command.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "/aliases/editor") || !strings.Contains(got, `base="vim"`) || !strings.Contains(got, `"helix"`) || !strings.Contains(got, `"nano"`) {
		t.Fatalf("conflict output = %q", got)
	}
	output.Reset()
	command = newWorkspaceCmd()
	command.SetOut(&output)
	command.SetArgs([]string{"resolve", "personal", "/aliases/editor", "--value", `"zed"`})
	if err = command.Execute(); err != nil {
		t.Fatal(err)
	}
	resolvedStore, err := registry.OpenDefault()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resolvedStore.Close() }()
	resolved, err := resolvedStore.LoadByName(ctx, "personal")
	if err != nil {
		t.Fatal(err)
	}
	remaining, err := resolvedStore.Conflicts(ctx, "personal")
	if err != nil || len(remaining) != 0 || resolved.State.Aliases["editor"] != "zed" {
		t.Fatalf("resolved=%#v conflicts=%#v error=%v", resolved.State.Aliases, remaining, err)
	}
}

func TestWorkspaceCreateDefaultsToCurrentDirectory(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(directory, "state"))
	cwd := t.TempDir()
	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })

	cmd := newWorkspaceCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"create", "--name", "personal"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("workspace create: %v", err)
	}
	out := bytes.Buffer{}
	cmd = newWorkspaceCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("workspace list: %v", err)
	}
	if !strings.Contains(out.String(), "personal\t"+cwd+"\n") {
		t.Fatalf("workspace list output = %q", out.String())
	}
}

func TestRootWorkspaceListDoesNotRequireCurrentWorkspace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("WS_ROOT", "")
	cwd := t.TempDir()
	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })
	if _, err := os.Stat(filepath.Join(cwd, "workspace.toml")); !os.IsNotExist(err) {
		t.Fatalf("test cwd unexpectedly contains workspace.toml: %v", err)
	}

	wsRoot = ""
	ws = nil
	t.Cleanup(func() {
		wsRoot = ""
		ws = nil
	})
	root := NewRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"workspace", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("ws workspace list outside a workspace: %v", err)
	}
}

func TestWorkspaceAccessSetDoesNotRequireCurrentWorkspace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("WS_ROOT", "")
	store, err := registry.OpenDefault()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err = store.EnsureNetwork(ctx, "arch"); err != nil {
		t.Fatal(err)
	}
	peer, err := device.Load(filepath.Join(t.TempDir(), "peer.key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddNetworkDevice(ctx, "asahi", peer.PublicKey(), registry.NetworkMember); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Create(ctx, "personal", t.TempDir(), &config.Workspace{Meta: config.Meta{Version: 1}}); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })
	wsRoot = ""
	ws = nil
	t.Cleanup(func() {
		wsRoot = ""
		ws = nil
	})
	root := NewRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"workspace", "access", "set", "personal", peer.ID(), registry.WorkspaceWriter})
	if err = root.Execute(); err != nil {
		t.Fatalf("workspace access set outside a workspace: %v", err)
	}
}

func TestExplorerDoesNotRequireCurrentWorkspace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("WS_ROOT", "")
	cwd := t.TempDir()
	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })

	wsRoot = ""
	ws = nil
	t.Cleanup(func() {
		wsRoot = ""
		ws = nil
	})
	root := NewRootCmd()
	explorer, _, err := root.Find([]string{"explorer"})
	if err != nil {
		t.Fatalf("find explorer command: %v", err)
	}
	if err := root.PersistentPreRunE(explorer, nil); err != nil {
		t.Fatalf("explorer pre-run outside a workspace: %v", err)
	}
}

func TestAgentAliasIsRemoved(t *testing.T) {
	root := NewRootCmd()
	if _, _, err := root.Find([]string{"agent"}); err == nil {
		t.Fatal("ws agent should not resolve to a command")
	}
}

func TestExplorerShellLaunchAliasIsRemoved(t *testing.T) {
	root := NewRootCmd()
	cmd, args, err := root.Find([]string{"explorer", "launch"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.ValidateArgs(args); err == nil {
		t.Fatal("ws explorer launch should fail")
	}
}
