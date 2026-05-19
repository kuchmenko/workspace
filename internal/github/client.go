package github

type Client interface {
	CurrentUser() (string, error)
	FetchRepos() ([]Repo, error)
	FetchActivity(username string) (map[string]int, error)
}
