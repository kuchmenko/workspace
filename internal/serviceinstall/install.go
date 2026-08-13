package serviceinstall

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type Options struct{ Listen, Endpoint, Name, StateDir, BinaryPath string }

func Validate(options Options) error {
	if options.Listen == "" || options.Endpoint == "" || options.Name == "" || options.StateDir == "" || options.BinaryPath == "" {
		return errors.New("listen, endpoint, name, state-dir, and binary-path are required")
	}
	if _, _, err := net.SplitHostPort(options.Listen); err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	endpoint, err := url.Parse(options.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.Port() == "" {
		return errors.New("advertised endpoint must be an HTTPS URL with host and port")
	}
	if !filepath.IsAbs(options.StateDir) || !filepath.IsAbs(options.BinaryPath) {
		return errors.New("state-dir and binary-path must be absolute")
	}
	return nil
}

func ServiceUnit(options Options) string {
	return `[Unit]
Description=ws LAN sync service
After=network-online.target
Wants=network-online.target

[Service]
User=ws
Group=ws
ExecStart=` + options.BinaryPath + ` sync service run --listen ` + options.Listen + ` --advertised-endpoint ` + options.Endpoint + ` --name ` + systemdEscape(options.Name) + ` --state-dir ` + options.StateDir + ` --updater-socket /run/ws-sync-updater.sock
Restart=on-failure
RestartSec=5s
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
RestrictNamespaces=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
ReadWritePaths=` + options.StateDir + `

[Install]
WantedBy=multi-user.target
`
}

func UpdaterSocketUnit() string {
	return `[Unit]
Description=ws sync privileged updater socket

[Socket]
ListenStream=/run/ws-sync-updater.sock
SocketUser=root
SocketGroup=ws
SocketMode=0660
Accept=yes

[Install]
WantedBy=sockets.target
`
}

func UpdaterServiceUnit(options Options) string {
	return `[Unit]
Description=ws sync privileged updater

[Service]
User=root
Group=root
StandardInput=socket
StandardOutput=socket
ExecStart=` + options.BinaryPath + ` sync service updater-serve --state-dir ` + options.StateDir + ` --binary-path ` + options.BinaryPath + `
NoNewPrivileges=yes
PrivateTmp=yes
ProtectHome=yes
ProtectSystem=strict
ReadWritePaths=` + filepath.Dir(options.BinaryPath) + ` ` + options.StateDir + `
`
}

func systemdEscape(value string) string { return strconv.Quote(value) }

func Install(options Options) error {
	if os.Geteuid() != 0 {
		return errors.New("service installation requires root")
	}
	if err := Validate(options); err != nil {
		return err
	}
	if err := ensureUser(); err != nil {
		return err
	}
	if err := os.MkdirAll(options.StateDir, 0o700); err != nil {
		return err
	}
	if err := run("chown", "-R", "ws:ws", options.StateDir); err != nil {
		return err
	}
	if err := copyExecutable(options.BinaryPath); err != nil {
		return err
	}
	units := map[string]string{"/etc/systemd/system/ws-sync.service": ServiceUnit(options), "/etc/systemd/system/ws-sync-updater.socket": UpdaterSocketUnit(), "/etc/systemd/system/ws-sync-updater@.service": UpdaterServiceUnit(options)}
	for path, body := range units {
		if err := atomicWrite(path, []byte(body), 0o644); err != nil {
			return err
		}
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "--now", "ws-sync-updater.socket"}, {"enable", "--now", "ws-sync.service"}} {
		if err := run("systemctl", args...); err != nil {
			return err
		}
	}
	return nil
}

func ensureUser() error {
	if err := exec.Command("id", "-u", "ws").Run(); err == nil {
		return nil
	}
	return run("useradd", "--system", "--user-group", "--home-dir", "/var/lib/ws", "--shell", "/usr/sbin/nologin", "ws")
}

func copyExecutable(destination string) error {
	source, err := os.Executable()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	return atomicWrite(destination, data, 0o755)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".ws-install-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err = file.Chmod(mode); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func run(name string, args ...string) error {
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}
