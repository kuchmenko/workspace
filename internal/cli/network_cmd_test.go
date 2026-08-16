package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	peernetwork "github.com/kuchmenko/workspace/internal/network"
	"github.com/kuchmenko/workspace/internal/registry"
	"github.com/spf13/cobra"
)

func TestNetworkConfirmationRequiresExplicitYes(t *testing.T) {
	for _, test := range []struct {
		input string
		want  bool
	}{{"yes\n", true}, {"y\n", true}, {"\n", false}, {"no\n", false}} {
		command := &cobra.Command{}
		command.SetIn(strings.NewReader(test.input))
		var output bytes.Buffer
		command.SetOut(&output)
		confirmed, err := networkConfirmation(command)("asahi", "1234-5678")
		if err != nil {
			t.Fatal(err)
		}
		if confirmed != test.want {
			t.Fatalf("input %q confirmed=%t", test.input, confirmed)
		}
		if !strings.Contains(output.String(), "asahi") || !strings.Contains(output.String(), "1234-5678") {
			t.Fatalf("confirmation output = %q", output.String())
		}
	}
}

func TestNetworkCommandsDoNotRequireWorkspace(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := NewRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"network", "status"})
	err := root.ExecuteContext(context.Background())
	if err == nil || strings.Contains(err.Error(), "no SQLite workspace") {
		t.Fatalf("network status error = %v", err)
	}
}

func TestNetworkServeUsesStablePeerPort(t *testing.T) {
	command := newNetworkServeCmd()
	flag := command.Flag("listen")
	if flag == nil || flag.DefValue != ":17337" {
		t.Fatalf("serve listen default = %v", flag)
	}
}

func TestNetworkStatusAlignsColumns(t *testing.T) {
	statuses := []peernetwork.Status{
		{Device: registry.DeviceRecord{Name: "archlinux", Role: registry.NetworkAdmin, Active: true}, Online: true, Endpoint: "local"},
		{Device: registry.DeviceRecord{Name: "asahi", Role: registry.NetworkAdmin, Active: true}, Online: true, Endpoint: "192.168.88.154:17337"},
	}
	var output bytes.Buffer
	if err := writeNetworkStatus(&output, statuses); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("status output = %q", output.String())
	}
	for _, column := range []string{"ROLE", "STATE", "ENDPOINT"} {
		headerIndex := strings.Index(lines[0], column)
		value := map[string]string{"ROLE": "admin", "STATE": "● online", "ENDPOINT": "local"}[column]
		valueIndex := strings.Index(lines[1], value)
		if headerIndex < 0 || valueIndex < 0 || utf8.RuneCountInString(lines[0][:headerIndex]) != utf8.RuneCountInString(lines[1][:valueIndex]) {
			t.Fatalf("column %s is not aligned:\n%s", column, output.String())
		}
	}
}
