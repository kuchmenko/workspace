package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Token struct {
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	Scope       string    `json:"scope"`
	CreatedAt   time.Time `json:"created_at"`
}

// ConfigDir returns the ws config directory (~/.config/ws).
func ConfigDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ws"), nil
}

// TokenPath returns the path to the token file.
func TokenPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "token"), nil
}

// LoadToken reads the stored token. Returns empty token and error if not found.
func LoadToken() (Token, error) {
	path, err := TokenPath()
	if err != nil {
		return Token{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Token{}, err
	}
	var t Token
	if err := json.Unmarshal(data, &t); err != nil {
		return Token{}, fmt.Errorf("invalid token file: %w", err)
	}
	if t.AccessToken == "" {
		return Token{}, fmt.Errorf("token file has empty access_token")
	}
	return t, nil
}

// SaveToken writes a token to disk with 0600 permissions.
func SaveToken(t Token) error {
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "token")
	return os.WriteFile(path, data, 0o600)
}

// DeleteToken removes the stored token.
func DeleteToken() error {
	path, err := TokenPath()
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// HasToken returns true if a valid token is stored.
func HasToken() bool {
	t, err := LoadToken()
	return err == nil && t.AccessToken != ""
}
