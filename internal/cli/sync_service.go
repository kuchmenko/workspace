package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/syncclient"
	"github.com/kuchmenko/workspace/internal/syncdiscovery"
	"github.com/kuchmenko/workspace/internal/syncservice"
	"github.com/spf13/cobra"
)

func independentServiceCommand(command *cobra.Command) *cobra.Command {
	command.Annotations = map[string]string{skipsWorkspaceAnnotation: "true"}
	return command
}

func newSyncServiceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "service", Short: "Use the explicit LAN sync service"}
	cmd.AddCommand(newServiceRunCmd(), newServicePairingCodeCmd(), newServicePairCmd(), newServiceStatusCmd(), newServiceImportCmd(), newServiceAttachCmd(), newServiceResolveCmd(), newServiceRevokeCmd(), newServiceDiscoverCmd(), newServiceInstallCmd(), newServiceUpgradeCmd(), newServiceUpdaterServeCmd())
	return cmd
}

func newServiceRunCmd() *cobra.Command {
	var listen, endpoint, stateDir, name, updaterSocket string
	cmd := independentServiceCommand(&cobra.Command{Use: "run", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		listener, err := net.Listen("tcp", listen)
		if err != nil {
			return err
		}
		defer listener.Close()
		advertised, err := validatedAdvertisedEndpoint(endpoint)
		if err != nil {
			return err
		}
		identity, err := syncservice.OpenIdentity(filepath.Join(stateDir, "identity"), []string{advertised.Hostname()})
		if err != nil {
			return err
		}
		store, err := syncservice.Open(filepath.Join(stateDir, "service.db"))
		if err != nil {
			return err
		}
		defer store.Close()
		server := syncservice.NewServer(store, identity, stateDir, updaterSocket)
		ref := store.ServiceRef()
		port, _ := strconv.Atoi(advertised.Port())
		discovery, err := syncdiscovery.Register(syncdiscovery.Record{Name: name, ServiceID: ref.ServiceID, Protocol: syncservice.ProtocolVersion, Fingerprint: identity.Fingerprint(), Endpoint: strings.TrimRight(endpoint, "/")}, port)
		if err != nil {
			return fmt.Errorf("advertising sync service: %w", err)
		}
		defer discovery.Shutdown()
		fmt.Fprintf(cmd.OutOrStdout(), "service %s name=%s listen=%s endpoint=%s fingerprint=%s\n", ref.ServiceID, name, listener.Addr(), endpoint, identity.Fingerprint())
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		done := make(chan error, 1)
		go func() { done <- server.Serve(listener) }()
		select {
		case err = <-done:
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return server.Shutdown(shutdown)
		}
	}})
	cmd.Flags().StringVar(&listen, "listen", "", "TCP host:port")
	cmd.Flags().StringVar(&endpoint, "advertised-endpoint", "", "advertised HTTPS endpoint")
	cmd.Flags().StringVar(&stateDir, "state-dir", "", "service state directory")
	cmd.Flags().StringVar(&name, "name", "ws sync", "service display name")
	cmd.Flags().StringVar(&updaterSocket, "updater-socket", "", "privileged updater Unix socket")
	_ = cmd.MarkFlagRequired("listen")
	_ = cmd.MarkFlagRequired("advertised-endpoint")
	_ = cmd.MarkFlagRequired("state-dir")
	return cmd
}

func newServicePairingCodeCmd() *cobra.Command {
	var endpoint, stateDir, role string
	var ttl time.Duration
	cmd := independentServiceCommand(&cobra.Command{Use: "pairing-code", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if role != string(syncservice.RoleAdmin) && role != string(syncservice.RoleClient) {
			return errors.New("role must be admin or client")
		}
		if stateDir == "" {
			client, _, err := pairedClient()
			if err != nil {
				return err
			}
			code, err := client.CreatePairing(cmd.Context(), role)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), code)
			return nil
		}
		if endpoint == "" {
			return errors.New("--endpoint is required with --state-dir")
		}
		store, err := syncservice.Open(filepath.Join(stateDir, "service.db"))
		if err != nil {
			return err
		}
		defer store.Close()
		identity, err := syncservice.OpenIdentity(filepath.Join(stateDir, "identity"), endpointNames(endpoint))
		if err != nil {
			return err
		}
		pairing, err := store.CreatePairing(syncservice.Role(role), ttl)
		if err != nil {
			return err
		}
		code := syncclient.PairingCode{Endpoint: strings.TrimRight(endpoint, "/"), ServiceID: store.ServiceRef().ServiceID, CAFingerprint: identity.Fingerprint(), Token: pairing.Token}
		fmt.Fprintln(cmd.OutOrStdout(), code.String())
		return nil
	}})
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "advertised HTTPS endpoint")
	cmd.Flags().StringVar(&stateDir, "state-dir", "", "local service state directory")
	cmd.Flags().StringVar(&role, "role", "client", "admin or client")
	cmd.Flags().DurationVar(&ttl, "ttl", 10*time.Minute, "pairing code lifetime")
	return cmd
}

func endpointNames(endpoint string) []string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil
	}
	return []string{parsed.Hostname()}
}

func validatedAdvertisedEndpoint(endpoint string) (*url.URL, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.Port() == "" {
		return nil, errors.New("advertised endpoint must be an HTTPS URL with host and port")
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && ip.IsUnspecified() {
		return nil, errors.New("wildcard address is not a valid advertised certificate name")
	}
	return parsed, nil
}

func newServicePairCmd() *cobra.Command {
	return independentServiceCommand(&cobra.Command{Use: "pair <ws1-code>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		code, err := syncclient.ParsePairingCode(args[0])
		if err != nil {
			return err
		}
		paths, err := syncclient.DefaultPaths()
		if err != nil {
			return err
		}
		machine, err := config.LoadMachineConfig()
		if err != nil {
			return err
		}
		name := machine.MachineName
		if name == "" {
			name, _ = os.Hostname()
			name = config.SanitizeMachineName(name)
		}
		credentials, err := syncclient.PairPersistent(cmd.Context(), code, name, paths.Attempt)
		if err != nil {
			return err
		}
		if err = syncclient.SaveCredentials(paths.Credentials, credentials); err != nil {
			return err
		}
		machine.Service = &config.MachineService{ID: credentials.ServiceID, Endpoint: credentials.Endpoint}
		if err = config.SaveMachineConfig(machine); err != nil {
			return err
		}
		if err = os.Remove(paths.Attempt); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "paired service=%s actor=%s role=%s\n", credentials.ServiceID, credentials.ActorID, credentials.Role)
		return nil
	}})
}

func pairedClient() (*syncclient.Client, syncclient.Paths, error) {
	paths, err := syncclient.DefaultPaths()
	if err != nil {
		return nil, paths, err
	}
	credentials, err := syncclient.LoadCredentials(paths.Credentials)
	if err != nil {
		return nil, paths, err
	}
	client, err := syncclient.New(credentials)
	return client, paths, err
}

func newServiceStatusCmd() *cobra.Command {
	return independentServiceCommand(&cobra.Command{Use: "status", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		client, _, err := pairedClient()
		if err != nil {
			return err
		}
		status, err := client.Status(cmd.Context())
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "service=%s actor=%s role=%s protocol=%d version=%s\n", status.ServiceID, status.ActorID, status.Role, status.ProtocolVersion, status.Version)
		return nil
	}})
}

func newServiceImportCmd() *cobra.Command {
	return &cobra.Command{Use: "import", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := refuseTrackedWorkspace(wsRoot); err != nil {
			return err
		}
		client, paths, err := pairedClient()
		if err != nil {
			return err
		}
		body, err := os.ReadFile(filepath.Join(wsRoot, "workspace.toml"))
		if err != nil {
			return err
		}
		response, err := client.Import(cmd.Context(), body)
		if err != nil {
			return err
		}
		store, err := syncclient.Open(paths.Database)
		if err != nil {
			return err
		}
		defer store.Close()
		if err = store.Initialize(cmd.Context(), response); err != nil {
			return err
		}
		return saveServiceBinding(wsRoot, response.State.WorkspaceID)
	}}
}

func newServiceAttachCmd() *cobra.Command {
	return independentServiceCommand(&cobra.Command{Use: "attach <workspace-id>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if wsRoot == "" {
			return errors.New("--root is required when attaching a workspace")
		}
		if err := refuseTrackedWorkspace(wsRoot); err != nil {
			return err
		}
		client, paths, err := pairedClient()
		if err != nil {
			return err
		}
		response, err := client.Current(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		store, err := syncclient.Open(paths.Database)
		if err != nil {
			return err
		}
		defer store.Close()
		if err = store.Attach(cmd.Context(), wsRoot, response); err != nil {
			return err
		}
		return saveServiceBinding(wsRoot, args[0])
	}})
}

func newServiceResolveCmd() *cobra.Command {
	return &cobra.Command{Use: "resolve", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		machine, err := config.LoadMachineConfig()
		if err != nil {
			return err
		}
		binding, ok, err := machine.Binding(wsRoot)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("workspace is not attached to a sync service")
		}
		client, paths, err := pairedClient()
		if err != nil {
			return err
		}
		store, err := syncclient.Open(paths.Database)
		if err != nil {
			return err
		}
		defer store.Close()
		response, err := client.ResolveWorkspace(cmd.Context(), store, binding.WorkspaceID, wsRoot)
		if err != nil {
			return err
		}
		if len(response.Conflicts) != 0 {
			for _, conflict := range response.Conflicts {
				fmt.Fprintf(cmd.OutOrStdout(), "service workspace conflict at %s\n", conflict.Path)
			}
			return errors.New("workspace remains conflicted")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "resolved workspace=%s revision=%d\n", binding.WorkspaceID, response.State.Revision)
		return nil
	}}
}

func newServiceRevokeCmd() *cobra.Command {
	return independentServiceCommand(&cobra.Command{Use: "revoke <actor-id>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		client, _, err := pairedClient()
		if err != nil {
			return err
		}
		return client.Revoke(cmd.Context(), args[0])
	}})
}

func saveServiceBinding(root, workspaceID string) error {
	machine, err := config.LoadMachineConfig()
	if err != nil {
		return err
	}
	if err = machine.AddBinding(root, workspaceID); err != nil {
		return err
	}
	return config.SaveMachineConfig(machine)
}

func refuseTrackedWorkspace(root string) error {
	path, err := filepath.EvalSymlinks(filepath.Join(root, "workspace.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	command := exec.Command("git", "-C", filepath.Dir(path), "ls-files", "--error-unmatch", path)
	if command.Run() == nil {
		return fmt.Errorf("workspace.toml is tracked by an owning Git repository; remove it from Git tracking yourself with 'git rm --cached %s' before granting the sync service authority", path)
	}
	return nil
}
