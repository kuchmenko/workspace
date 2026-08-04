package alias

import (
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	valid := []string{"a", "A", "_", "project-2", "my_alias", "Z9"}
	for _, name := range valid {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) returned %v", name, err)
		}
	}

	invalid := []string{
		"", "two words", "line\nbreak", "name=value", "quo'te", "semi;colon",
		"$(touch hacked)", "`touch hacked`", "control\x01byte", "2fast", "dot.name",
	}
	for _, name := range invalid {
		if err := ValidateName(name); err == nil {
			t.Errorf("ValidateName(%q) returned nil", name)
		}
	}
}

func TestRenderZshIsDeterministicAndSafe(t *testing.T) {
	resolved := []Resolved{
		{Name: "z-last", Kind: TargetProject, Path: "/work/last"},
		{Name: "bad;echo injected", Kind: TargetProject, Path: "/work/bad"},
		{Name: "missing", Kind: TargetUnknown, Path: "/work/missing"},
		{Name: "first", Kind: TargetProject, Path: "/work/O'Brien project"},
	}
	want := "# ws aliases — generated, do not edit\n" +
		"alias first='cd /work/O'\\''Brien project'\n" +
		"alias z-last='cd /work/last'\n"

	got := RenderZsh(resolved)
	if got != want {
		t.Fatalf("RenderZsh() = %q, want %q", got, want)
	}
	if again := RenderZsh(resolved); again != got {
		t.Fatalf("second rendering differs: %q != %q", again, got)
	}
	if !strings.Contains(got, "Brien project") {
		t.Fatal("destination path was omitted")
	}
	if strings.Contains(got, "metrics.json") || strings.Contains(got, "XDG_STATE_HOME") || strings.Contains(got, "ws metrics") {
		t.Fatalf("generated aliases contain a runtime metrics hook: %q", got)
	}
}
