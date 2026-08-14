package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/serviceinstall"
	"github.com/kuchmenko/workspace/internal/syncclient"
	"github.com/kuchmenko/workspace/internal/syncdiscovery"
	"github.com/kuchmenko/workspace/internal/syncservice"
	"github.com/kuchmenko/workspace/internal/updater"
	"github.com/spf13/cobra"
)

func newServiceDiscoverCmd() *cobra.Command {
	var timeout time.Duration
	cmd := independentServiceCommand(&cobra.Command{Use: "discover", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if timeout <= 0 {
			return errors.New("timeout must be positive")
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
		defer cancel()
		records, err := syncdiscovery.Browse(ctx)
		if err != nil {
			return err
		}
		for _, record := range records {
			fmt.Fprintf(cmd.OutOrStdout(), "name=%q service=%s protocol=%d endpoint=%s fingerprint=%s\n", record.Name, record.ServiceID, record.Protocol, record.Endpoint, record.Fingerprint)
		}
		return nil
	}})
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Second, "discovery browse duration")
	return cmd
}

func newServiceInstallCmd() *cobra.Command {
	hostname, _ := os.Hostname()
	hostname = strings.TrimSuffix(hostname, ".")
	var options serviceinstall.Options
	cmd := independentServiceCommand(&cobra.Command{Use: "install", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if os.Geteuid() != 0 {
			return errors.New("service installation requires root")
		}
		if err := serviceinstall.Validate(options); err != nil {
			return err
		}
		identity, err := syncservice.OpenIdentity(filepath.Join(options.StateDir, "identity"), endpointNames(options.Endpoint))
		if err != nil {
			return err
		}
		store, err := syncservice.Open(filepath.Join(options.StateDir, "service.db"))
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		pairing, err := store.CreatePairing(syncservice.RoleAdmin, 10*time.Minute)
		if err != nil {
			return err
		}
		if err = store.Close(); err != nil {
			return err
		}
		if err = serviceinstall.Install(options); err != nil {
			return err
		}
		code := syncclient.PairingCode{Endpoint: strings.TrimRight(options.Endpoint, "/"), ServiceID: store.ServiceRef().ServiceID, CAFingerprint: identity.Fingerprint(), Token: pairing.Token}
		fmt.Fprintf(cmd.OutOrStdout(), "installed ws sync service\ninitial admin pairing code (expires %s):\n%s\n", pairing.ExpiresAt.Format(time.RFC3339), code.String())
		return nil
	}})
	cmd.Flags().StringVar(&options.Listen, "listen", "0.0.0.0:47321", "TCP host:port")
	cmd.Flags().StringVar(&options.Endpoint, "advertised-endpoint", "https://"+hostname+".local:47321", "advertised HTTPS endpoint")
	cmd.Flags().StringVar(&options.Name, "name", hostname, "service display name")
	cmd.Flags().StringVar(&options.StateDir, "state-dir", "/var/lib/ws", "service state directory")
	cmd.Flags().StringVar(&options.BinaryPath, "binary-path", "/usr/local/bin/ws", "installed binary path")
	return cmd
}

func newServiceUpgradeCmd() *cobra.Command {
	var version string
	cmd := independentServiceCommand(&cobra.Command{Use: "upgrade", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if !syncservice.ValidUpgradeVersion(version) {
			return errors.New("version must be latest or vMAJOR.MINOR.PATCH")
		}
		client, _, err := pairedClient()
		if err != nil {
			return err
		}
		result, err := client.Upgrade(cmd.Context(), version)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "upgrade accepted target=%s backup=%s\n", result.Version, result.Backup)
		deadline := time.Now().Add(2 * time.Minute)
		var status syncclient.StatusResponse
		for time.Now().Before(deadline) {
			time.Sleep(2 * time.Second)
			poll, pollErr := client.Status(cmd.Context())
			if pollErr == nil {
				status = poll
				if status.Version == result.Version {
					fmt.Fprintf(cmd.OutOrStdout(), "service returned version=%s\n", status.Version)
					return nil
				}
			}
		}
		return errors.New("service did not return at the requested version within 2m")
	}})
	cmd.Flags().StringVar(&version, "version", "latest", "latest or vMAJOR.MINOR.PATCH")
	return cmd
}

func newServiceUpdaterServeCmd() *cobra.Command {
	var stateDir, binaryPath string
	cmd := independentServiceCommand(&cobra.Command{Use: "updater-serve", Hidden: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return updater.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), stateDir, binaryPath)
	}})
	cmd.Flags().StringVar(&stateDir, "state-dir", "/var/lib/ws", "service state directory")
	cmd.Flags().StringVar(&binaryPath, "binary-path", "/usr/local/bin/ws", "installed binary path")
	return cmd
}
