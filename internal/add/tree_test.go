package add

import (
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
)

func TestGroupKey(t *testing.T) {
	cases := []struct {
		name      string
		s         Suggestion
		wantKey   string
		wantLabel string
		wantOrder int
	}{
		{
			name:      "clipboard alone pins to top",
			s:         Suggestion{Sources: []SourceKind{SourceClipboard}, Name: "x"},
			wantKey:   "_clip",
			wantLabel: "Clipboard",
			wantOrder: 0,
		},
		{
			name:      "manual alone",
			s:         Suggestion{Sources: []SourceKind{SourceManual}, Name: "x"},
			wantKey:   "_manual",
			wantLabel: "Manual",
			wantOrder: 0,
		},
		{
			name:      "disk-only repo",
			s:         Suggestion{Sources: []SourceKind{SourceDisk}, DiskPath: "/tmp/foo"},
			wantKey:   "_disk",
			wantLabel: "Local (unregistered)",
			wantOrder: 1,
		},
		{
			name:      "github repo with owner → group key by owner",
			s:         Suggestion{Sources: []SourceKind{SourceGitHub}, InferredGrp: "kuchmenko"},
			wantKey:   "gh:kuchmenko",
			wantLabel: "kuchmenko",
			wantOrder: 2,
		},
		{
			name: "mixed disk+github lands in the github bucket — disk presence is a row-level highlight",
			s: Suggestion{
				Sources:     []SourceKind{SourceDisk, SourceGitHub},
				InferredGrp: "myorg",
				DiskPath:    "/tmp/foo",
			},
			wantKey:   "gh:myorg",
			wantLabel: "myorg",
			wantOrder: 2,
		},
		{
			name: "mixed clipboard+github → still github (the source that gives us org context)",
			s: Suggestion{
				Sources:     []SourceKind{SourceClipboard, SourceGitHub},
				InferredGrp: "neat",
			},
			wantKey:   "gh:neat",
			wantLabel: "neat",
			wantOrder: 2,
		},
		{
			name:      "github source without owner → other",
			s:         Suggestion{Sources: []SourceKind{SourceGitHub}},
			wantKey:   "_other",
			wantLabel: "Other",
			wantOrder: 3,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k, l, o := groupKey(c.s)
			if k != c.wantKey || l != c.wantLabel || o != c.wantOrder {
				t.Errorf("got (%q, %q, %d), want (%q, %q, %d)",
					k, l, o, c.wantKey, c.wantLabel, c.wantOrder)
			}
		})
	}
}

func TestBuildBrowseRows_GroupsAndOrders(t *testing.T) {
	view := []Suggestion{
		{Name: "a", Sources: []SourceKind{SourceGitHub}, InferredGrp: "myorg"},
		{Name: "b", Sources: []SourceKind{SourceGitHub}, InferredGrp: "kuchmenko"},
		{Name: "c", Sources: []SourceKind{SourceGitHub}, InferredGrp: "kuchmenko"},
		{Name: "d", Sources: []SourceKind{SourceClipboard}, RemoteURL: "git@github.com:foo/bar.git"},
		{Name: "e", Sources: []SourceKind{SourceDisk}, DiskPath: "/tmp/e"},
	}

	rows := buildBrowseRows(view)

	// Expected order:
	//   [Clipboard (1)]  d
	//   [Local... (1)]   e
	//   [kuchmenko (2)]  b, c
	//   [myorg (1)]      a
	wantHeaderOrder := []string{"Clipboard", "Local (unregistered)", "kuchmenko", "myorg"}
	var headerSeen []string
	for _, r := range rows {
		if r.kind == rowGroup {
			// r.text contains the styled label + count; the label
			// is the leftmost word(s). Just check the prefix.
			for _, want := range wantHeaderOrder {
				if containsIgnoringStyles(r.text, want) {
					headerSeen = append(headerSeen, want)
					break
				}
			}
		}
	}
	if !sliceEq(headerSeen, wantHeaderOrder) {
		t.Errorf("header order: got %v, want %v", headerSeen, wantHeaderOrder)
	}

	// Item count: 5 items + 4 headers = 9 rows.
	if len(rows) != 9 {
		t.Errorf("rows: got %d, want 9", len(rows))
	}
}

func TestBuildBrowseRows_EmptyInput(t *testing.T) {
	rows := buildBrowseRows(nil)
	if len(rows) != 0 {
		t.Errorf("empty view should produce 0 rows, got %d", len(rows))
	}
}

func TestWindowAround(t *testing.T) {
	cases := []struct {
		name     string
		cursor   int
		total    int
		size     int
		wantS, wantE int
	}{
		{"total fits in window", 5, 10, 16, 0, 10},
		{"cursor near start", 2, 100, 16, 0, 16},
		{"cursor in middle", 50, 100, 16, 42, 58},
		{"cursor near end", 98, 100, 16, 84, 100},
		{"negative cursor (no items selected)", -1, 100, 16, 0, 16},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, e := windowAround(c.cursor, c.total, c.size)
			if s != c.wantS || e != c.wantE {
				t.Errorf("got (%d, %d), want (%d, %d)", s, e, c.wantS, c.wantE)
			}
		})
	}
}

func TestOwnerRepoFromRemote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"git@github.com:foo/bar.git", "foo/bar"},
		{"https://github.com/foo/bar.git", "foo/bar"},
		{"https://github.com/Foo/Bar", "foo/bar"}, // case-folded
		{"ssh://git@github.com/foo/bar.git", "foo/bar"},
		{"https://gitlab.com/group/sub/proj", "group/sub"}, // owner/repo only
		{"", ""},
		{"not a url", ""},
		{"https://github.com/", ""},
	}
	for _, c := range cases {
		got := ownerRepoFromRemote(c.in)
		if got != c.want {
			t.Errorf("ownerRepoFromRemote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestKnownRemotesFromWorkspace(t *testing.T) {
	ws := &config.Workspace{
		Projects: map[string]config.Project{
			"a":     {Remote: "git@github.com:foo/bar.git", Path: "personal/a"},
			"b":     {Remote: "https://gitlab.com/team/proj", Path: "work/b"},
			"empty": {Path: "personal/empty"}, // no remote → skipped
		},
	}
	got := knownRemotesFromWorkspace(ws)
	if got["foo/bar"] != "personal/a" {
		t.Errorf("foo/bar: got %q", got["foo/bar"])
	}
	if got["team/proj"] != "work/b" {
		t.Errorf("team/proj: got %q", got["team/proj"])
	}
	if _, ok := got["empty"]; ok {
		t.Error("empty-remote entry should not be in the map")
	}
}

// containsIgnoringStyles checks whether `needle` appears in `haystack`
// after stripping ANSI escape sequences. lipgloss-rendered headers
// embed escape codes that defeat plain substring matching.
func containsIgnoringStyles(haystack, needle string) bool {
	stripped := stripANSI(haystack)
	for i := 0; i+len(needle) <= len(stripped); i++ {
		if stripped[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func stripANSI(s string) string {
	// Minimal ANSI strip: drop everything between ESC[ and the
	// terminating letter. Good enough for tests that only need the
	// human-readable substring.
	var out []rune
	in := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			in = true
		case in:
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				in = false
			}
		default:
			out = append(out, r)
		}
	}
	return string(out)
}
