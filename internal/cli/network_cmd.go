package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/kuchmenko/workspace/internal/device"
	peernetwork "github.com/kuchmenko/workspace/internal/network"
	"github.com/kuchmenko/workspace/internal/registry"
	"github.com/spf13/cobra"
)

func newNetworkCmd() *cobra.Command {
	command := &cobra.Command{Use: "network", Short: "Manage the trusted device network"}
	command.AddCommand(
		newNetworkPairCmd(),
		newNetworkJoinCmd(),
		newNetworkStatusCmd(),
		newNetworkServeCmd(),
		newNetworkRoleCmd(),
		newNetworkRemoveCmd(),
		newNetworkConflictsCmd(),
		newNetworkResolveCmd(),
	)
	return command
}

func newNetworkPairCmd() *cobra.Command {
	var name string
	command := &cobra.Command{
		Use:   "pair",
		Short: "Pair a nearby device using a one-time code",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			store, identity, err := openNetworkNode()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			name, err = networkDeviceName(name)
			if err != nil {
				return err
			}
			ctx, stop := networkSignalContext(command.Context())
			defer stop()
			result, err := peernetwork.Pair(ctx, peernetwork.PairOptions{
				Store: store, Identity: identity, Name: name, Role: registry.NetworkMember,
				Ready: func(code, _ string) {
					fmt.Fprintf(command.OutOrStdout(), "Pairing code: %s\nWaiting for nearby device…\n", code)
				},
				Confirm: networkConfirmation(command),
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(command.OutOrStdout(), "Paired with %s (%s).\n", terminalText(result.Peer.Name), shortDeviceID(result.Peer.ID))
			return nil
		},
	}
	command.Flags().StringVar(&name, "name", "", "this device name (default: hostname)")
	return command
}

func newNetworkJoinCmd() *cobra.Command {
	var name string
	command := &cobra.Command{
		Use:   "join <code>",
		Short: "Join the device showing this pairing code",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			store, identity, err := openNetworkNode()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			name, err = networkDeviceName(name)
			if err != nil {
				return err
			}
			ctx, stop := networkSignalContext(command.Context())
			defer stop()
			result, err := peernetwork.Join(ctx, peernetwork.JoinOptions{
				Store: store, Identity: identity, Name: name, Code: args[0], Confirm: networkConfirmation(command),
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(command.OutOrStdout(), "Paired with %s (%s).\n", terminalText(result.Peer.Name), shortDeviceID(result.Peer.ID))
			return nil
		},
	}
	command.Flags().StringVar(&name, "name", "", "this device name (default: hostname)")
	return command
}

func newNetworkStatusCmd() *cobra.Command {
	var name string
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "status",
		Short: "Show trusted devices and their availability",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			store, identity, err := openNetworkNode()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			name, err = networkDeviceName(name)
			if err != nil {
				return err
			}
			statuses, err := peernetwork.NetworkStatus(command.Context(), store, identity, name, 1500*time.Millisecond)
			if err != nil {
				return err
			}
			if jsonOutput {
				encoder := json.NewEncoder(command.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(statuses)
			}
			return writeNetworkStatus(command.OutOrStdout(), statuses)
		},
	}
	command.Flags().StringVar(&name, "name", "", "this device name (default: hostname)")
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	return command
}

func writeNetworkStatus(output io.Writer, statuses []peernetwork.Status) error {
	table := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "DEVICE\tROLE\tSTATE\tENDPOINT")
	for _, status := range statuses {
		state := "○ offline"
		if status.Online {
			state = "● online"
		}
		if !status.Device.Active {
			state = "× removed"
		}
		endpoint := status.Endpoint
		if endpoint == "" {
			endpoint = "-"
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", terminalText(status.Device.Name), terminalText(status.Device.Role), state, terminalText(endpoint))
	}
	return table.Flush()
}

func newNetworkServeCmd() *cobra.Command {
	var name, listen string
	command := &cobra.Command{
		Use:   "serve",
		Short: "Stay available to trusted devices",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			store, identity, err := openNetworkNode()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			name, err = networkDeviceName(name)
			if err != nil {
				return err
			}
			ctx, stop := networkSignalContext(command.Context())
			defer stop()
			return peernetwork.Serve(ctx, peernetwork.ServeOptions{
				Store: store, Identity: identity, Name: name, ListenAddress: listen,
				Ready: func(endpoint string) { fmt.Fprintf(command.OutOrStdout(), "Device available at %s.\n", endpoint) },
			})
		},
	}
	command.Flags().StringVar(&name, "name", "", "this device name (default: hostname)")
	command.Flags().StringVar(&listen, "listen", peernetwork.DefaultListenAddress, "peer listen address")
	return command
}

func newNetworkRoleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "role <device> <admin|member>",
		Short: "Change a device network role",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, args []string) error {
			store, _, err := openNetworkNode()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			id, err := networkDeviceID(command.Context(), store, args[0])
			if err != nil {
				return err
			}
			_, err = store.SetNetworkRole(command.Context(), id, args[1])
			return err
		},
	}
}

func newNetworkRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <device>",
		Short: "Remove a device from the trusted network",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			store, _, err := openNetworkNode()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			id, err := networkDeviceID(command.Context(), store, args[0])
			if err != nil {
				return err
			}
			_, err = store.RemoveNetworkDevice(command.Context(), id)
			return err
		},
	}
}

func newNetworkConflictsCmd() *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "conflicts",
		Short: "Show a concurrent network membership conflict",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			store, _, err := openNetworkNode()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			conflict, err := store.NetworkConflict(command.Context())
			if err != nil {
				return err
			}
			if jsonOutput {
				encoder := json.NewEncoder(command.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(conflict)
			}
			fmt.Fprintf(command.OutOrStdout(), "conflict=%s base=%s\n", conflict.ID, conflict.Base)
			for _, head := range conflict.Heads {
				fmt.Fprintf(command.OutOrStdout(), "head=%s epoch=%d action=%s device=%s signer=%s\n", head.ID, head.Epoch, head.Action, terminalText(head.DeviceName), shortDeviceID(head.SignerID))
			}
			return nil
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	return command
}

func newNetworkResolveCmd() *cobra.Command {
	var selected string
	command := &cobra.Command{
		Use:   "resolve <conflict>",
		Short: "Resolve a concurrent network membership conflict",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			store, _, err := openNetworkNode()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			state, err := store.ResolveNetworkConflict(command.Context(), args[0], selected)
			if err != nil {
				return err
			}
			fmt.Fprintf(command.OutOrStdout(), "network=%s epoch=%d\n", state.ID, state.Epoch)
			return nil
		},
	}
	command.Flags().StringVar(&selected, "from", "", "conflicting head whose membership state to keep")
	_ = command.MarkFlagRequired("from")
	return command
}

func openNetworkNode() (*registry.Store, device.Identity, error) {
	store, err := registry.OpenDefault()
	if err != nil {
		return nil, device.Identity{}, err
	}
	path, err := device.DefaultPath()
	if err != nil {
		_ = store.Close()
		return nil, device.Identity{}, err
	}
	identity, err := device.Load(path)
	if err != nil {
		_ = store.Close()
		return nil, device.Identity{}, err
	}
	return store, identity, nil
}

func networkDeviceName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name != "" {
		return name, nil
	}
	return os.Hostname()
}

func networkConfirmation(command *cobra.Command) peernetwork.Confirm {
	reader := bufio.NewReader(command.InOrStdin())
	return func(peerName, authentication string) (bool, error) {
		fmt.Fprintf(command.OutOrStdout(), "Device: %s\nVerification: %s\nConfirm pairing? [y/N] ", terminalText(peerName), terminalText(authentication))
		answer, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return false, err
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		return answer == "y" || answer == "yes", nil
	}
}

func networkDeviceID(ctx context.Context, store *registry.Store, selector string) (string, error) {
	state, err := store.Network(ctx)
	if err != nil {
		return "", err
	}
	var matched string
	for _, record := range state.Devices {
		if record.Name != selector && !strings.HasPrefix(record.ID, selector) {
			continue
		}
		if matched != "" && matched != record.ID {
			return "", fmt.Errorf("device %q is ambiguous", selector)
		}
		matched = record.ID
	}
	if matched == "" {
		return "", fmt.Errorf("device %q not found", selector)
	}
	return matched, nil
}

func shortDeviceID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func networkSignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}
