package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ClientID for the ws GitHub OAuth App.
// This is NOT a secret — it identifies the app, not the user.
// Register at: https://github.com/settings/applications/new
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

// DeviceFlow runs the GitHub OAuth Device Authorization flow.
// It prints a user code, opens the browser, and polls for authorization.
func DeviceFlow() (Token, error) {
	// Step 1: Request device code
	dc, err := requestDeviceCode()
	if err != nil {
		return Token{}, err
	}

	// Step 2: Show code and open browser
	fmt.Printf("\n  Open this URL in your browser:\n")
	fmt.Printf("  %s\n\n", dc.VerificationURI)
	fmt.Printf("  Enter code: %s\n\n", dc.UserCode)

	openBrowser(dc.VerificationURI)

	// Step 3: Poll for token
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
			continue // network or decode error — retry next tick
		}
		token, retry, retryDelay, err := interpretTokenResponse(tr)
		if !retry {
			return token, err
		}
		interval += retryDelay
	}
	return Token{}, fmt.Errorf("timed out waiting for authorization")
}

// requestToken posts the device-code grant exchange and decodes the
// response. Returns ok=false on network or decode failure (the caller
// retries); ok=true with the parsed body otherwise.
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

// interpretTokenResponse maps a tokenResponse into the loop driver's
// next action. Returns:
//
//   - retry=false: the caller exits with (token, err). On success
//     err is nil; on terminal failure err is the caller-facing
//     reason ("expired_token", "access_denied", …).
//   - retry=true: the caller continues polling, optionally adding
//     `retryDelay` to the interval (for "slow_down" responses).
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
