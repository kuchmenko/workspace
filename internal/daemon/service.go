package daemon

import (
	"context"
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const serviceLabel = "com.kuchmenko.ws.daemon"

func InstallService(ctx context.Context, name string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path, manager, err := servicePath(runtime.GOOS, home)
	if err != nil {
		return err
	}
	if _, err = exec.LookPath(manager); err != nil {
		return fmt.Errorf("daemon installation requires %s: %w", manager, err)
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		return err
	}
	environment := serviceEnvironment(home)
	logPath := filepath.Join(environment["XDG_STATE_HOME"], "ws", "daemon.log")
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		if err = os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
			return err
		}
	}
	if err = os.WriteFile(path, []byte(renderService(runtime.GOOS, binary, name, home, logPath, environment)), 0o600); err != nil {
		return err
	}
	return installNativeService(ctx, path)
}

func installNativeService(ctx context.Context, path string) error {
	if runtime.GOOS == "linux" {
		if err := serviceCommand(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		if err := serviceCommand(ctx, "systemctl", "--user", "enable", "ws-daemon.service"); err != nil {
			return err
		}
		return serviceCommand(ctx, "systemctl", "--user", "restart", "ws-daemon.service")
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	if err := stopNativeService(ctx); err != nil {
		return err
	}
	if err := serviceCommand(ctx, "launchctl", "enable", domain+"/"+serviceLabel); err != nil {
		return err
	}
	return serviceCommand(ctx, "launchctl", "bootstrap", domain, path)
}

func UninstallService(ctx context.Context) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path, _, err := servicePath(runtime.GOOS, home)
	if err != nil {
		return err
	}
	if _, err = os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err = stopNativeService(ctx); err != nil {
		return err
	}
	if err = os.Remove(path); err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		return serviceCommand(ctx, "systemctl", "--user", "daemon-reload")
	}
	return nil
}

func stopNativeService(ctx context.Context) error {
	if runtime.GOOS == "linux" {
		return serviceCommand(ctx, "systemctl", "--user", "disable", "--now", "ws-daemon.service")
	}
	target := "gui/" + strconv.Itoa(os.Getuid()) + "/" + serviceLabel
	loaded, err := launchAgentLoaded(ctx, target)
	if err != nil {
		return err
	}
	if !loaded {
		return nil
	}
	if err := serviceCommand(ctx, "launchctl", "bootout", target); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		loaded, err := launchAgentLoaded(ctx, target)
		if err != nil {
			return err
		}
		if !loaded {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for launchd to remove %s: %w", target, ctx.Err())
		case <-ticker.C:
		}
	}
}

func servicePath(platform, home string) (string, string, error) {
	switch platform {
	case "linux":
		return filepath.Join(home, ".config", "systemd", "user", "ws-daemon.service"), "systemctl", nil
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist"), "launchctl", nil
	default:
		return "", "", fmt.Errorf("daemon installation is not supported on %s", platform)
	}
}

func serviceEnvironment(home string) map[string]string {
	environment := map[string]string{"HOME": home, "PATH": os.Getenv("PATH")}
	if environment["PATH"] == "" {
		environment["PATH"] = filepath.Join(home, ".local/bin") + ":/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"
	}
	for key, fallback := range map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_STATE_HOME":  filepath.Join(home, ".local/state"),
	} {
		environment[key] = os.Getenv(key)
		if environment[key] == "" {
			environment[key] = fallback
		}
	}
	if value := os.Getenv("XDG_DATA_HOME"); value != "" {
		environment["XDG_DATA_HOME"] = value
	}
	return environment
}

func serviceCommand(ctx context.Context, command string, args ...string) error {
	output, err := exec.CommandContext(ctx, command, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", command, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func launchAgentLoaded(ctx context.Context, target string) (bool, error) {
	output, err := exec.CommandContext(ctx, "launchctl", "print", target).CombinedOutput()
	if err == nil {
		return true, nil
	}
	if strings.Contains(string(output), "Could not find service") {
		return false, nil
	}
	return false, fmt.Errorf("launchctl print %s: %w: %s", target, err, strings.TrimSpace(string(output)))
}

func renderService(platform, binary, name, home, logPath string, environment map[string]string) string {
	keys := []string{"HOME", "PATH", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME"}
	if platform == "linux" {
		var body strings.Builder
		body.WriteString("[Unit]\nDescription=Workspace background sync\n\n[Service]\nType=simple\n")
		fmt.Fprintf(&body, "ExecStart=%s daemon run --name %s\nWorkingDirectory=%%h\n", systemdQuote(strings.ReplaceAll(binary, "$", "$$")), systemdQuote(strings.ReplaceAll(name, "$", "$$")))
		for _, key := range keys {
			if value, exists := environment[key]; exists {
				fmt.Fprintf(&body, "Environment=%s\n", systemdQuote(key+"="+value))
			}
		}
		body.WriteString("Restart=on-failure\nRestartSec=5\n\n[Install]\nWantedBy=default.target\n")
		return body.String()
	}
	var body strings.Builder
	body.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict>\n")
	fmt.Fprintf(&body, "<key>Label</key><string>%s</string>\n<key>ProgramArguments</key><array>", serviceLabel)
	for _, arg := range []string{binary, "daemon", "run", "--name", name} {
		fmt.Fprintf(&body, "<string>%s</string>", html.EscapeString(arg))
	}
	body.WriteString("</array>\n<key>EnvironmentVariables</key><dict>")
	for _, key := range keys {
		if value, exists := environment[key]; exists {
			fmt.Fprintf(&body, "<key>%s</key><string>%s</string>", key, html.EscapeString(value))
		}
	}
	printf := func(key, value string) {
		fmt.Fprintf(&body, "<key>%s</key><string>%s</string>\n", key, html.EscapeString(value))
	}
	body.WriteString("</dict>\n")
	printf("WorkingDirectory", home)
	printf("StandardOutPath", logPath)
	printf("StandardErrorPath", logPath)
	body.WriteString("<key>RunAtLoad</key><true/>\n<key>KeepAlive</key><true/>\n<key>ThrottleInterval</key><integer>5</integer>\n</dict></plist>\n")
	return body.String()
}

func systemdQuote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(value) + `"`
}
