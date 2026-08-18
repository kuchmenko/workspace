package registry

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
)

func TestDecodeSnapshotRejectsProjectRemoteControls(t *testing.T) {
	tests := []struct {
		name    string
		project snapshotProject
	}{
		{name: "remote", project: snapshotProject{Remote: "repo\x1b]8;;https://example.com\a", Mirrors: map[string]string{}}},
		{name: "mirror", project: snapshotProject{Remote: "https://example.com/repo.git", Mirrors: map[string]string{"backup": "repo\nunsafe"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(snapshot{
				Version: 1,
				Groups:  map[string]config.Group{},
				Projects: map[string]snapshotProject{
					"app": test.project,
				},
				Aliases: map[string]string{},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = decodeSnapshot(body); err == nil || !strings.Contains(err.Error(), "control characters") {
				t.Fatalf("decodeSnapshot error = %v", err)
			}
		})
	}
}
