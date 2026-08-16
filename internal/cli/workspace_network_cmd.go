package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kuchmenko/workspace/internal/device"
	peernetwork "github.com/kuchmenko/workspace/internal/network"
	"github.com/kuchmenko/workspace/internal/registry"
	"github.com/spf13/cobra"
)

func newWorkspaceShareCmd() *cobra.Command {
	var recipients, role string
	command := &cobra.Command{
		Use:   "share <workspace>",
		Short: "Choose which trusted devices can synchronize a workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			store, identity, err := openNetworkNode()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			current, err := store.Access(command.Context(), args[0])
			if err != nil {
				return err
			}
			policy, err := sharedPolicy(command, store, identity.ID(), current, recipients, role)
			if err != nil {
				return err
			}
			workspace, err := store.SetAccess(command.Context(), args[0], policy)
			if err != nil {
				return err
			}
			fmt.Fprintf(command.OutOrStdout(), "workspace=%s mode=%s epoch=%d\n", workspace.Name, policy.Mode, workspace.Epoch)
			return nil
		},
	}
	command.Flags().StringVar(&recipients, "with", "", "local, all, or comma-separated device names")
	command.Flags().StringVar(&role, "role", registry.WorkspaceWriter, "default role: admin, writer, or replica")
	_ = command.MarkFlagRequired("with")
	return command
}

func sharedPolicy(command *cobra.Command, store *registry.Store, localID string, current registry.AccessPolicy, recipients, role string) (registry.AccessPolicy, error) {
	if !validWorkspaceRole(role) {
		return registry.AccessPolicy{}, errors.New("workspace role must be admin, writer, or replica")
	}
	switch strings.TrimSpace(recipients) {
	case registry.AccessLocal:
		return registry.AccessPolicy{Mode: registry.AccessLocal, Roles: map[string]string{localID: registry.WorkspaceAdmin}}, nil
	case registry.AccessAll:
		if role == registry.WorkspaceAdmin {
			return registry.AccessPolicy{}, errors.New("all devices cannot be workspace admins by default")
		}
		return registry.AccessPolicy{Mode: registry.AccessAll, DefaultRole: role, Roles: copyRoles(current.Roles, localID)}, nil
	case "":
		return registry.AccessPolicy{}, errors.New("workspace recipients are required")
	}
	roles := copyRoles(current.Roles, localID)
	for _, selector := range strings.Split(recipients, ",") {
		deviceID, err := networkDeviceID(command.Context(), store, strings.TrimSpace(selector))
		if err != nil {
			return registry.AccessPolicy{}, err
		}
		roles[deviceID] = role
	}
	return registry.AccessPolicy{Mode: registry.AccessSelected, Roles: roles}, nil
}

func copyRoles(source map[string]string, localID string) map[string]string {
	roles := map[string]string{}
	for deviceID, role := range source {
		if role == registry.WorkspaceAdmin {
			roles[deviceID] = role
		}
	}
	if roles[localID] == "" {
		roles[localID] = registry.WorkspaceAdmin
	}
	return roles
}

func validWorkspaceRole(role string) bool {
	return role == registry.WorkspaceAdmin || role == registry.WorkspaceWriter || role == registry.WorkspaceReplica
}

func newWorkspaceAccessCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "access <workspace>",
		Short: "Show or change workspace access",
		Args:  cobra.ExactArgs(1),
		RunE:  runWorkspaceAccess,
	}
	command.AddCommand(newWorkspaceAccessSetCmd(), newWorkspaceAccessRemoveCmd())
	return command
}

func runWorkspaceAccess(command *cobra.Command, args []string) error {
	store, err := registry.OpenDefault()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	policy, err := store.Access(command.Context(), args[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(command.OutOrStdout(), "MODE\tDEFAULT ROLE\n%s\t%s\n", policy.Mode, displayDash(policy.DefaultRole))
	deviceNames := workspaceDeviceNames(command, store)
	ids := make([]string, 0, len(policy.Roles))
	for id := range policy.Roles {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return deviceNames[ids[left]] < deviceNames[ids[right]] })
	for _, id := range ids {
		fmt.Fprintf(command.OutOrStdout(), "%s\t%s\n", displayDevice(deviceNames[id], id), policy.Roles[id])
	}
	for _, id := range policy.Denied {
		fmt.Fprintf(command.OutOrStdout(), "%s\tdenied\n", displayDevice(deviceNames[id], id))
	}
	return nil
}

func newWorkspaceAccessSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <workspace> <device> <admin|writer|replica>",
		Short: "Set one device workspace role",
		Args:  cobra.ExactArgs(3),
		RunE: func(command *cobra.Command, args []string) error {
			if !validWorkspaceRole(args[2]) {
				return errors.New("workspace role must be admin, writer, or replica")
			}
			store, _, err := openNetworkNode()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			id, err := networkDeviceID(command.Context(), store, args[1])
			if err != nil {
				return err
			}
			policy, err := store.Access(command.Context(), args[0])
			if err != nil {
				return err
			}
			if policy.Mode == registry.AccessLocal {
				policy.Mode = registry.AccessSelected
			}
			policy.Roles[id] = args[2]
			policy.Denied = removeDevice(policy.Denied, id)
			_, err = store.SetAccess(command.Context(), args[0], policy)
			return err
		},
	}
}

func removeDevice(devices []string, removed string) []string {
	result := devices[:0]
	for _, deviceID := range devices {
		if deviceID != removed {
			result = append(result, deviceID)
		}
	}
	return result
}

func newWorkspaceAccessRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <workspace> <device>",
		Short: "Remove one device from selected workspace access",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, args []string) error {
			store, _, err := openNetworkNode()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			id, err := networkDeviceID(command.Context(), store, args[1])
			if err != nil {
				return err
			}
			policy, err := store.Access(command.Context(), args[0])
			if err != nil {
				return err
			}
			if policy.Mode == registry.AccessAll {
				return errors.New("switch workspace sharing to selected devices before removing one device")
			}
			delete(policy.Roles, id)
			_, err = store.SetAccess(command.Context(), args[0], policy)
			return err
		},
	}
}

func newWorkspaceAvailableCmd() *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "available",
		Short: "List shared workspaces advertised by online devices",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			store, identity, err := openNetworkNode()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			name, err := networkDeviceName("")
			if err != nil {
				return err
			}
			available, err := peernetwork.Available(command.Context(), store, identity, name, 1500*time.Millisecond)
			if err != nil {
				return err
			}
			if jsonOutput {
				encoder := json.NewEncoder(command.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(available)
			}
			table := tabwriter.NewWriter(command.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(table, "WORKSPACE\tROLE\tDEVICE\tENDPOINT")
			for _, item := range available {
				fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", item.Name, item.Role, item.DeviceName, item.Endpoint)
			}
			return table.Flush()
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	return command
}

func newWorkspaceAttachCmd() *cobra.Command {
	var root, localName string
	command := &cobra.Command{
		Use:   "attach <workspace>",
		Short: "Attach an available shared workspace at a local root",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			store, identity, err := openNetworkNode()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			name, err := networkDeviceName("")
			if err != nil {
				return err
			}
			available, err := peernetwork.Available(command.Context(), store, identity, name, 1500*time.Millisecond)
			if err != nil {
				return err
			}
			source, err := selectAvailableWorkspace(available, args[0])
			if err != nil {
				return err
			}
			if localName == "" {
				localName = source.Name
			}
			if err = os.MkdirAll(root, 0o755); err != nil {
				return err
			}
			bundle, err := peernetwork.Fetch(command.Context(), source, store, identity, name)
			if err != nil {
				return err
			}
			workspace, err := store.AttachFrom(command.Context(), localName, root, bundle, source.DeviceID)
			if err != nil {
				return err
			}
			fmt.Fprintf(command.OutOrStdout(), "workspace=%s root=%s device=%s\n", workspace.Name, workspace.Root, source.DeviceName)
			return nil
		},
	}
	command.Flags().StringVar(&root, "root", "", "machine-local workspace root")
	command.Flags().StringVar(&localName, "name", "", "machine-local workspace name")
	_ = command.MarkFlagRequired("root")
	return command
}

func selectAvailableWorkspace(available []peernetwork.AvailableWorkspace, selector string) (peernetwork.AvailableWorkspace, error) {
	var match peernetwork.AvailableWorkspace
	for _, candidate := range available {
		if candidate.Name != selector && !strings.HasPrefix(candidate.WorkspaceID, selector) {
			continue
		}
		if match.WorkspaceID != "" && match.WorkspaceID != candidate.WorkspaceID {
			return peernetwork.AvailableWorkspace{}, fmt.Errorf("workspace %q is ambiguous", selector)
		}
		match = candidate
	}
	if match.WorkspaceID == "" {
		return peernetwork.AvailableWorkspace{}, fmt.Errorf("workspace %q is not available", selector)
	}
	return match, nil
}

func newWorkspaceSyncCmd() *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "sync [workspace]",
		Short: "Synchronize attached workspace registries with online peers",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			return runWorkspaceSync(command, args, jsonOutput)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	return command
}

func runWorkspaceSync(command *cobra.Command, args []string, jsonOutput bool) error {
	store, identity, err := openNetworkNode()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	name, err := networkDeviceName("")
	if err != nil {
		return err
	}
	workspaces, err := synchronizedWorkspaces(command, store, args)
	if err != nil {
		return err
	}
	peers, err := peernetwork.DiscoverPeers(command.Context(), store, identity.ID(), 1500*time.Millisecond)
	if err != nil {
		return err
	}
	results, failures, err := synchronizeWorkspacePeers(command, store, identity, name, workspaces, peers)
	if err != nil {
		return err
	}
	if err = writeWorkspaceSyncResults(command, results, jsonOutput); err != nil {
		return err
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	if len(results) == 0 {
		return errors.New("no authorized attached workspace peer exists")
	}
	return nil
}

func synchronizeWorkspacePeers(command *cobra.Command, store *registry.Store, identity device.Identity, name string, workspaces []registry.Workspace, online []peernetwork.PeerEndpoint) ([]peernetwork.SyncResult, []string, error) {
	return synchronizeWorkspacePeersContext(command.Context(), store, identity, name, workspaces, online)
}

func synchronizeWorkspacePeersContext(ctx context.Context, store *registry.Store, identity device.Identity, name string, workspaces []registry.Workspace, online []peernetwork.PeerEndpoint) ([]peernetwork.SyncResult, []string, error) {
	state, err := store.Network(ctx)
	if err != nil {
		return nil, nil, err
	}
	endpoints := map[string]string{}
	for _, peer := range online {
		endpoints[peer.Device.ID] = peer.Endpoint
	}
	var results []peernetwork.SyncResult
	var failures []string
	for _, workspace := range workspaces {
		for _, target := range state.Devices {
			if err = ctx.Err(); err != nil {
				return nil, nil, err
			}
			if target.ID == identity.ID() || !target.Active {
				continue
			}
			if _, allowed := store.ExportFor(ctx, workspace.Name, target.ID); allowed != nil {
				continue
			}
			result, failure := synchronizeWorkspacePeerContext(ctx, store, identity, name, workspace.Name, target, endpoints[target.ID])
			if err = ctx.Err(); err != nil {
				return nil, nil, err
			}
			results = append(results, result)
			if failure != "" {
				failures = append(failures, failure)
			}
		}
	}
	return results, failures, nil
}

func synchronizeWorkspacePeerContext(ctx context.Context, store *registry.Store, identity device.Identity, name, workspace string, target registry.DeviceRecord, endpoint string) (peernetwork.SyncResult, string) {
	if endpoint == "" {
		return peernetwork.SyncResult{Workspace: workspace, Device: target.Name, Status: "unavailable"}, fmt.Sprintf("%s/%s: unavailable", workspace, target.Name)
	}
	result, err := peernetwork.Sync(ctx, workspace, endpoint, target, store, identity, name)
	if err != nil {
		if peernetwork.IsUnavailable(err) {
			return peernetwork.SyncResult{Workspace: workspace, Device: target.Name, Status: "unavailable"}, fmt.Sprintf("%s/%s: %v", workspace, target.Name, err)
		}
		return peernetwork.SyncResult{Workspace: workspace, Device: target.Name, Status: "rejected"}, fmt.Sprintf("%s/%s: %v", workspace, target.Name, err)
	}
	return result, ""
}

func writeWorkspaceSyncResults(command *cobra.Command, results []peernetwork.SyncResult, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(command.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(results)
	}
	for _, result := range results {
		fmt.Fprintf(command.OutOrStdout(), "%s\t%s\t%s\n", result.Workspace, result.Device, result.Status)
	}
	return nil
}

func synchronizedWorkspaces(command *cobra.Command, store *registry.Store, args []string) ([]registry.Workspace, error) {
	if len(args) == 1 {
		workspace, err := store.LoadByName(command.Context(), args[0])
		if err != nil {
			return nil, err
		}
		return []registry.Workspace{workspace}, nil
	}
	return store.List(command.Context())
}

func workspaceDeviceNames(command *cobra.Command, store *registry.Store) map[string]string {
	names := map[string]string{}
	state, err := store.Network(command.Context())
	if err != nil {
		return names
	}
	for _, record := range state.Devices {
		names[record.ID] = record.Name
	}
	return names
}

func displayDevice(name, id string) string {
	if name == "" {
		return shortDeviceID(id)
	}
	return name
}

func displayDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
