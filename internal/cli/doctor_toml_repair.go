package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
)

var branchTablePattern = regexp.MustCompile(`^\s*\[\[projects\.([^.]*)\.branches\]\]\s*$`)
var projectTablePattern = regexp.MustCompile(`^\s*\[projects\.([^.]*)\]\s*$`)
var keyPattern = regexp.MustCompile(`^\s*([A-Za-z0-9_-]+)\s*=`)

func repairWorkspaceTOML(root string) error {
	path := filepath.Join(root, "workspace.toml")
	original, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	repaired, changed := repairDuplicatedBranchKeys(string(original))
	if !changed {
		return fmt.Errorf("no safe TOML repair found")
	}
	backupPath := path + ".doctor-bak"
	if err := os.WriteFile(backupPath, original, 0o644); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}
	if err := os.WriteFile(path, []byte(repaired), 0o644); err != nil {
		return err
	}
	if _, err := config.Load(root); err != nil {
		_ = os.WriteFile(path, original, 0o644)
		return fmt.Errorf("repair produced invalid workspace.toml, rolled back: %w", err)
	}
	return nil
}

type tomlRepairState struct {
	project  string
	inBranch bool
	seen     map[string]int
	out      []string
	changed  bool
}

func repairDuplicatedBranchKeys(input string) (string, bool) {
	lines := strings.SplitAfter(input, "\n")
	state := tomlRepairState{seen: map[string]int{}, out: make([]string, 0, len(lines)+4)}
	for _, line := range lines {
		state.consume(line)
	}
	return strings.Join(state.out, ""), state.changed
}

func (s *tomlRepairState) consume(line string) {
	trimmed := strings.TrimSpace(line)
	if m := branchTablePattern.FindStringSubmatch(trimmed); m != nil {
		s.startBranch(m[1], line)
		return
	}
	if strings.HasPrefix(trimmed, "[") {
		s.startTable(trimmed, line)
		return
	}
	if s.inBranch {
		if s.consumeBranchKey(line) {
			return
		}
	}
	s.out = append(s.out, line)
}

func (s *tomlRepairState) startBranch(project, line string) {
	s.project = project
	s.inBranch = true
	s.seen = map[string]int{}
	s.out = append(s.out, line)
}

func (s *tomlRepairState) startTable(trimmed, line string) {
	s.inBranch = false
	s.seen = map[string]int{}
	if m := projectTablePattern.FindStringSubmatch(trimmed); m != nil {
		s.project = m[1]
	}
	s.out = append(s.out, line)
}

func (s *tomlRepairState) consumeBranchKey(line string) bool {
	m := keyPattern.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	key := m[1]
	idx, duplicate := s.seen[key]
	if duplicate && key == "name" && s.project != "" {
		s.out = append(s.out, "\n", "    [[projects."+s.project+".branches]]\n")
		s.seen = map[string]int{}
		s.changed = true
	} else if duplicate {
		s.out[idx] = repairDuplicateBranchLine(s.out[idx], line)
		s.changed = true
		return true
	}
	s.seen[key] = len(s.out)
	return false
}

func repairDuplicateBranchLine(prev, next string) string {
	if strings.TrimSpace(strings.Split(prev, "=")[0]) == "machines" && strings.TrimSpace(strings.Split(next, "=")[0]) == "machines" {
		prefix, prevValues, suffix, ok := splitArrayLine(prev)
		_, nextValues, _, nextOK := splitArrayLine(next)
		if ok && nextOK {
			return prefix + strings.Join(mergeStringArrays(prevValues, nextValues), ", ") + suffix
		}
	}
	return next
}

func splitArrayLine(line string) (prefix, values, suffix string, ok bool) {
	start := strings.Index(line, "[")
	end := strings.Index(line, "]")
	if start < 0 || end < start {
		return "", "", "", false
	}
	return line[:start+1], line[start+1 : end], line[end:], true
}

func mergeStringArrays(a, b string) []string {
	seen := map[string]bool{}
	for _, raw := range append(strings.Split(a, ","), strings.Split(b, ",")...) {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		seen[v] = true
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
