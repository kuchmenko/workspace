package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestSetFavorite_Idempotent(t *testing.T) {
	p := &Project{}
	if !p.SetFavorite(true) {
		t.Error("first SetFavorite(true) should report changed")
	}
	if p.SetFavorite(true) {
		t.Error("second SetFavorite(true) should be no-op")
	}
	if !p.SetFavorite(false) {
		t.Error("SetFavorite(false) on favorited project should report changed")
	}
	if p.SetFavorite(false) {
		t.Error("second SetFavorite(false) should be no-op")
	}
}

func TestFavorite_RoundTrip_OmitWhenFalse(t *testing.T) {
	const src = `
[meta]
version = 1
root = "/ws"

[projects.starred]
remote = "git@github.com:me/starred.git"
path = "personal/starred"
status = "active"
category = "personal"
favorite = true

[projects.plain]
remote = "git@github.com:me/plain.git"
path = "personal/plain"
status = "active"
category = "personal"
`
	dir := writeWS(t, src)
	ws, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !ws.Projects["starred"].Favorite {
		t.Error("starred.Favorite should be true after Load")
	}
	if ws.Projects["plain"].Favorite {
		t.Error("plain.Favorite should be false after Load")
	}
	if err := Save(dir, ws); err != nil {
		t.Fatalf("Save: %v", err)
	}
	body := readWS(t, dir)
	starredBlock, plainBlock := isolateProject(t, body, "starred"), isolateProject(t, body, "plain")
	if !strings.Contains(starredBlock, "favorite = true") {
		t.Errorf("starred block missing `favorite = true`:\n%s", starredBlock)
	}
	if strings.Contains(plainBlock, "favorite") {
		t.Errorf("plain block should omit `favorite` when false:\n%s", plainBlock)
	}
}

func isolateProject(t *testing.T, body, name string) string {
	t.Helper()
	header := "[projects." + name + "]"
	start := strings.Index(body, header)
	if start < 0 {
		t.Fatalf("project %q section not found in:\n%s", name, body)
	}
	rest := body[start+len(header):]
	bestNext := -1
	for i := 0; i < len(rest); i++ {
		if rest[i] != '\n' {
			continue
		}
		j := i + 1
		for j < len(rest) && (rest[j] == ' ' || rest[j] == '\t') {
			j++
		}
		if strings.HasPrefix(rest[j:], "[projects.") {
			bestNext = i
			break
		}
	}
	if bestNext < 0 {
		return body[start:]
	}
	return body[start : start+len(header)+bestNext]
}

func TestAgentDefaultView_FallsBackToAll(t *testing.T) {
	cases := []struct {
		raw, want string
	}{
		{"", AgentViewAll},
		{"all", AgentViewAll},
		{"favorites", AgentViewFavorites},
		{"garbage", AgentViewAll},
	}
	for _, tc := range cases {
		ws := &Workspace{Agent: AgentConfig{DefaultView: tc.raw}}
		if got := ws.AgentDefaultView(); got != tc.want {
			t.Errorf("raw=%q: want %q, got %q", tc.raw, tc.want, got)
		}
	}
}

func TestSetAgentDefaultView_NormalizesAndReportsChange(t *testing.T) {
	ws := &Workspace{}
	if ws.SetAgentDefaultView("all") {
		t.Error(`SetAgentDefaultView("all") on empty should be no-op (canonical is "")`)
	}
	if !ws.SetAgentDefaultView("favorites") {
		t.Error(`SetAgentDefaultView("favorites") should report changed`)
	}
	if ws.Agent.DefaultView != "favorites" {
		t.Errorf("want stored value 'favorites', got %q", ws.Agent.DefaultView)
	}
	if !ws.SetAgentDefaultView("garbage") {
		t.Error(`SetAgentDefaultView("garbage") flips back to "" (changed=true)`)
	}
	if ws.Agent.DefaultView != "" {
		t.Errorf("unknown values should normalize to empty; got %q", ws.Agent.DefaultView)
	}
}

func TestAgentConfig_RoundTrip(t *testing.T) {
	const src = `
[meta]
version = 1
root = "/ws"

[agent]
default_view = "favorites"

`
	dir := writeWS(t, src)
	ws, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := ws.AgentDefaultView(); got != AgentViewFavorites {
		t.Errorf("want favorites view post-Load, got %q", got)
	}
	if err := Save(dir, ws); err != nil {
		t.Fatalf("Save: %v", err)
	}
	body := readWS(t, dir)
	if !strings.Contains(body, `default_view = "favorites"`) {
		t.Errorf("Save lost agent.default_view:\n%s", body)
	}
}

func TestAgentConfig_OmitWhenEmpty(t *testing.T) {
	ws := &Workspace{
		Meta:     Meta{Version: 1, Root: "/ws"},
		Projects: map[string]Project{},
	}
	dir := t.TempDir()
	if err := Save(dir, ws); err != nil {
		t.Fatalf("Save: %v", err)
	}
	body := readWS(t, dir)
	if strings.Contains(body, "[agent]") || strings.Contains(body, "default_view") {
		t.Errorf("empty AgentConfig should omit the [agent] block entirely:\n%s", body)
	}
}

func TestMirrors_RoundTrip_OmitWhenEmpty(t *testing.T) {
	const src = `
[meta]
version = 1
root = "/ws"

[projects.mirrored]
remote = "git@codeberg.org:me/mirrored.git"
path = "personal/mirrored"
status = "active"
category = "personal"

[projects.mirrored.mirrors]
github = "git@github.com:me/mirrored.git"

[projects.plain]
remote = "git@codeberg.org:me/plain.git"
path = "personal/plain"
status = "active"
category = "personal"
`
	dir := writeWS(t, src)
	ws, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string]string{"github": "git@github.com:me/mirrored.git"}
	if !reflect.DeepEqual(ws.Projects["mirrored"].Mirrors, want) {
		t.Errorf("mirrored.Mirrors = %v, want %v", ws.Projects["mirrored"].Mirrors, want)
	}
	if len(ws.Projects["plain"].Mirrors) != 0 {
		t.Errorf("plain.Mirrors should be empty, got %v", ws.Projects["plain"].Mirrors)
	}
	if err := Save(dir, ws); err != nil {
		t.Fatalf("Save: %v", err)
	}
	ws2, err := Load(dir)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if !reflect.DeepEqual(ws2.Projects["mirrored"].Mirrors, want) {
		t.Errorf("Mirrors lost in round-trip: %v", ws2.Projects["mirrored"].Mirrors)
	}
	plainBlock := isolateProject(t, readWS(t, dir), "plain")
	if strings.Contains(plainBlock, "mirrors") {
		t.Errorf("plain block should omit `mirrors` when empty:\n%s", plainBlock)
	}
}
