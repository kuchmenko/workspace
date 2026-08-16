package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

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
