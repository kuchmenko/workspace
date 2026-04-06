package github

// Client abstracts GitHub API access.
type Client interface {
	CurrentUser() (string, error)
	FetchRepos() ([]Repo, error)
	FetchActivity(username string) (map[string]int, error)
}
