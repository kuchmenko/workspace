package serviceinstall

import (
	"strings"
	"testing"
)

func TestUnitsAndValidation(t *testing.T) {
	options := Options{Listen: "0.0.0.0:47321", Endpoint: "https://home.local:47321", Name: "home ws", StateDir: "/var/lib/ws", BinaryPath: "/usr/local/bin/ws"}
	if err := Validate(options); err != nil {
		t.Fatal(err)
	}
	service := ServiceUnit(options)
	for _, directive := range []string{"User=ws", "ProtectSystem=strict", "ReadWritePaths=/var/lib/ws", "--advertised-endpoint https://home.local:47321", "Restart=on-failure"} {
		if !strings.Contains(service, directive) {
			t.Fatalf("missing %q", directive)
		}
	}
	socket := UpdaterSocketUnit()
	for _, directive := range []string{"SocketUser=root", "SocketGroup=ws", "SocketMode=0660", "Accept=yes"} {
		if !strings.Contains(socket, directive) {
			t.Fatalf("missing %q", directive)
		}
	}
	updater := UpdaterServiceUnit(options)
	for _, directive := range []string{"User=root", "StandardInput=socket", "StandardOutput=socket", "updater-serve", "--state-dir /var/lib/ws", "--binary-path /usr/local/bin/ws", "ReadWritePaths=/usr/local/bin /var/lib/ws"} {
		if !strings.Contains(updater, directive) {
			t.Fatalf("missing %q", directive)
		}
	}
	options.Endpoint = "http://bad"
	if Validate(options) == nil {
		t.Fatal("accepted invalid endpoint")
	}
}
