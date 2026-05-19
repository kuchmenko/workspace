package github

import (
	"fmt"

	"github.com/kuchmenko/workspace/internal/auth"
)

func ResolveClient() (Client, error) {
	token, err := auth.LoadToken()
	if err == nil && token.AccessToken != "" {
		return NewHTTPClient(token.AccessToken), nil
	}
	return nil, fmt.Errorf("no GitHub authentication found — run `ws auth login`")
}
