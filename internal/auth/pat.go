package auth

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// PromptPAT reads a GitHub Personal Access Token from stdin and validates it.
func PromptPAT() (Token, error) {
	fmt.Println("\n  Create a token at: https://github.com/settings/tokens")
	fmt.Println("  Required scopes: repo, read:user, read:org")
	fmt.Print("\n  Paste token: ")

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return Token{}, fmt.Errorf("reading token: %w", err)
	}

	token := strings.TrimSpace(input)
	if token == "" {
		return Token{}, fmt.Errorf("empty token")
	}

	// Validate by making a test API call
	fmt.Print("  Validating... ")
	if err := validateToken(token); err != nil {
		return Token{}, err
	}
	fmt.Println("valid!")

	return Token{
		AccessToken: token,
		TokenType:   "bearer",
		Scope:       "repo,read:user,read:org",
		CreatedAt:   time.Now(),
	}, nil
}

func validateToken(token string) error {
	req, err := http.NewRequest("GET", "https://api.github.com/user", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("API request failed: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("invalid token (HTTP %d)", resp.StatusCode)
	}
	return nil
}
