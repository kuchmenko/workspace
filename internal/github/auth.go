package github

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Token struct {
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	Scope       string    `json:"scope"`
	CreatedAt   time.Time `json:"created_at"`
}

func ConfigDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ws"), nil
}

func TokenPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "token"), nil
}

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

func HasToken() bool {
	t, err := LoadToken()
	return err == nil && t.AccessToken != ""
}

const ClientID = "Iv23liLjbULITnvRegRh"

const (
	deviceCodeURL = "https://github.com/login/device/code"
	tokenURL      = "https://github.com/login/oauth/access_token"
	scopes        = "repo read:user read:org"
)

type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
}

func DeviceFlow() (Token, error) {
	dc, err := requestDeviceCode()
	if err != nil {
		return Token{}, err
	}

	fmt.Printf("\n  Open this URL in your browser:\n")
	fmt.Printf("  %s\n\n", dc.VerificationURI)
	fmt.Printf("  Enter code: %s\n\n", dc.UserCode)

	openBrowser(dc.VerificationURI)

	fmt.Printf("  Waiting for authorization...")
	token, err := pollForToken(dc)
	if err != nil {
		return Token{}, err
	}

	fmt.Printf(" done!\n")
	return token, nil
}

func requestDeviceCode() (deviceCodeResponse, error) {
	data := url.Values{
		"client_id": {ClientID},
		"scope":     {scopes},
	}

	req, err := http.NewRequest(http.MethodPost, deviceCodeURL, strings.NewReader(data.Encode()))
	if err != nil {
		return deviceCodeResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return deviceCodeResponse{}, fmt.Errorf("requesting device code: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return deviceCodeResponse{}, fmt.Errorf("device code request failed (%d): %s", resp.StatusCode, string(body))
	}

	var dc deviceCodeResponse
	if err := json.Unmarshal(body, &dc); err != nil {
		return deviceCodeResponse{}, fmt.Errorf("parsing device code response: %w", err)
	}
	return dc, nil
}

func pollForToken(dc deviceCodeResponse) (Token, error) {
	interval := time.Duration(dc.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(interval)
		tr, ok := requestToken(dc)
		if !ok {
			continue
		}
		token, retry, retryDelay, err := interpretTokenResponse(tr)
		if !retry {
			return token, err
		}
		interval += retryDelay
	}
	return Token{}, fmt.Errorf("timed out waiting for authorization")
}

func requestToken(dc deviceCodeResponse) (tokenResponse, bool) {
	data := url.Values{
		"client_id":   {ClientID},
		"device_code": {dc.DeviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}
	req, err := http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return tokenResponse{}, false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return tokenResponse{}, false
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return tokenResponse{}, false
	}
	return tr, true
}

func interpretTokenResponse(tr tokenResponse) (token Token, retry bool, retryDelay time.Duration, err error) {
	switch tr.Error {
	case "":
		return Token{
			AccessToken: tr.AccessToken,
			TokenType:   tr.TokenType,
			Scope:       tr.Scope,
			CreatedAt:   time.Now(),
		}, false, 0, nil
	case "authorization_pending":
		return Token{}, true, 0, nil
	case "slow_down":
		return Token{}, true, 5 * time.Second, nil
	case "expired_token":
		return Token{}, false, 0, fmt.Errorf("device code expired, please try again")
	case "access_denied":
		return Token{}, false, 0, fmt.Errorf("authorization denied by user")
	}
	return Token{}, false, 0, fmt.Errorf("auth error: %s", tr.Error)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		return
	}
	cmd.Start()
}

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
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/user", nil)
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

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("invalid token (HTTP %d)", resp.StatusCode)
	}
	return nil
}
