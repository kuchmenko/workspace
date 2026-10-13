package daemon

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestServiceInstallUninstallPreservesWorkspaceData(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("native user services require Linux or macOS")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "custom config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "custom state"))
	path, manager, err := servicePath(runtime.GOOS, home)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, "bin")
	if err = os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\nif [ \"$1\" = print ]; then echo 'Could not find service'; exit 1; fi\nexit 0\n"
	if runtime.GOOS == "darwin" {
		// bootout returns before launchd removes the job; bootstrap must wait.
		stub = `#!/bin/sh
state=absent
if [ -f "$HOME/job-state" ]; then read -r state < "$HOME/job-state"; fi
case "$1" in
print)
  case "$state" in
  absent) echo 'Could not find service'; exit 113 ;;
  running) echo 'state = running' ;;
  0) echo absent > "$HOME/job-state"; echo 'Could not find service'; exit 113 ;;
  *) echo "$((state - 1))" > "$HOME/job-state"; echo 'state = SIGTERMed' ;;
  esac ;;
bootout) echo 2 > "$HOME/job-state" ;;
bootstrap)
  if [ "$state" != absent ]; then echo 'Input/output error'; exit 5; fi
  echo running > "$HOME/job-state" ;;
esac
exit 0
`
	}
	if err = os.WriteFile(filepath.Join(bin, manager), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	sentinel := filepath.Join(home, "workspace-data")
	if err = os.WriteFile(sentinel, []byte("keep workspace and credentials\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = InstallService(context.Background(), "test device"); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "test device") || !strings.Contains(string(body), "custom state") || !strings.Contains(string(body), "custom config") {
			t.Fatalf("service lost device or XDG paths: %s", body)
		}
	}
	if err = os.WriteFile(filepath.Join(bin, manager), []byte("#!/bin/sh\necho permission-denied; exit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = UninstallService(context.Background()); err == nil {
		t.Fatal("uninstall succeeded despite failure to stop service")
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatalf("failed uninstall removed service file: %v", err)
	}
	if err = os.WriteFile(filepath.Join(bin, manager), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = UninstallService(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("service remains: %v", err)
	}
	if body, err := os.ReadFile(sentinel); err != nil || string(body) != "keep workspace and credentials\n" {
		t.Fatalf("uninstall changed workspace data: %q, %v", body, err)
	}
}

func TestServiceDefinitionsPreserveSpecialCharacters(t *testing.T) {
	binary := `/home/user space/bin/ws%$"&`
	name := `Mac "dev" & $HOME%`
	environment := map[string]string{"HOME": "/home/user space", "XDG_STATE_HOME": `/state space/"&%$`}
	linux := renderService("linux", binary, name, environment["HOME"], "/log", environment)
	if !strings.Contains(linux, `ExecStart="/home/user space/bin/ws%%$$\"&" daemon run --name "Mac \"dev\" & $$HOME%%"`) {
		t.Fatalf("systemd arguments changed: %s", linux)
	}
	if !strings.Contains(linux, `Environment="XDG_STATE_HOME=/state space/\"&%%$"`) {
		t.Fatalf("systemd environment changed: %s", linux)
	}
	mac := renderService("darwin", binary, name, environment["HOME"], "/log", environment)
	decoder := xml.NewDecoder(strings.NewReader(mac))
	values := map[string]bool{}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if element, ok := token.(xml.StartElement); ok && element.Name.Local == "string" {
			var value string
			if err := decoder.DecodeElement(&value, &element); err != nil {
				t.Fatal(err)
			}
			values[value] = true
		}
	}
	for _, value := range []string{binary, name, environment["XDG_STATE_HOME"]} {
		if !values[value] {
			t.Fatalf("plist lost literal %q: %s", value, mac)
		}
	}
}

func TestSystemdAcceptsServiceDefinition(t *testing.T) {
	parser, err := exec.LookPath("systemd-analyze")
	if runtime.GOOS != "linux" || err != nil {
		t.Skip("systemd parser unavailable")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	path := filepath.Join(home, "ws-daemon.service")
	body := renderService("linux", binary, `test "device" $HOME%`, home, "", map[string]string{"HOME": home})
	if err = os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(parser, "--user", "verify", path).CombinedOutput(); err != nil {
		t.Fatalf("systemd rejected service: %v: %s", err, output)
	}
}
