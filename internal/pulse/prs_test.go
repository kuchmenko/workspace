package pulse

import "testing"

func TestRepoFullNameFromURL(t *testing.T) {
	cases := map[string]string{
		"https://api.github.com/repos/kuchmenko/workspace": "kuchmenko/workspace",
		"https://api.github.com/repos/acme/api":            "acme/api",
		"https://github.com/kuchmenko/workspace":           "", // wrong host, ignored
		"":                                                 "",
	}
	for in, want := range cases {
		if got := repoFullNameFromURL(in); got != want {
			t.Errorf("repoFullNameFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGroupPRs_OrgRepoSort(t *testing.T) {
	prs := []PR{
		{Org: "kuchmenko", Repo: "kuchmenko/workspace", Number: 12},
		{Org: "kuchmenko", Repo: "kuchmenko/workspace", Number: 42},
		{Org: "acme", Repo: "acme/api", Number: 7},
		{Org: "kuchmenko", Repo: "kuchmenko/dotfiles", Number: 3},
	}
	groups := groupPRs(prs)
	if len(groups) != 3 {
		t.Fatalf("len(groups) = %d, want 3", len(groups))
	}
	// acme < kuchmenko alphabetically
	if groups[0].Org != "acme" {
		t.Errorf("groups[0].Org = %q, want acme", groups[0].Org)
	}
	// Within kuchmenko, dotfiles < workspace
	if groups[1].Repo != "kuchmenko/dotfiles" {
		t.Errorf("groups[1].Repo = %q, want kuchmenko/dotfiles", groups[1].Repo)
	}
	// PRs within a repo sorted by Number desc
	wsGroup := groups[2]
	if wsGroup.PRs[0].Number != 42 || wsGroup.PRs[1].Number != 12 {
		t.Errorf("workspace PRs not sorted desc: %+v", wsGroup.PRs)
	}
}

func TestPercentEncode(t *testing.T) {
	cases := map[string]string{
		"is:pr is:open author:@me": "is%3Apr%20is%3Aopen%20author%3A%40me",
		"plain":                    "plain",
		"a/b":                      "a%2Fb",
	}
	for in, want := range cases {
		if got := percentEncode(in); got != want {
			t.Errorf("percentEncode(%q) = %q, want %q", in, got, want)
		}
	}
}
