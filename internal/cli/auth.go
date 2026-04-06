package cli

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/kuchmenko/workspace/internal/auth"
	"github.com/spf13/cobra"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage GitHub authentication",
	}

	cmd.AddCommand(
		newAuthLoginCmd(),
		newAuthLogoutCmd(),
		newAuthStatusCmd(),
	)

	return cmd
}

func newAuthLoginCmd() *cobra.Command {
	var usePAT bool

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with GitHub",
		RunE: func(cmd *cobra.Command, args []string) error {
			var token auth.Token
			var err error

			if usePAT {
				token, err = auth.PromptPAT()
			} else {
				token, err = auth.DeviceFlow()
			}
			if err != nil {
				return err
			}

			if err := auth.SaveToken(token); err != nil {
				return fmt.Errorf("saving token: %w", err)
			}

			path, _ := auth.TokenPath()
			fmt.Printf("\n  Authenticated! Token saved to %s\n", path)
			return nil
		},
	}

	cmd.Flags().BoolVar(&usePAT, "pat", false, "use Personal Access Token instead of device flow")
	return cmd
}

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove stored authentication",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !auth.HasToken() {
				fmt.Println("  Not authenticated.")
				return nil
			}
			if err := auth.DeleteToken(); err != nil {
				return err
			}
			fmt.Println("  Token removed.")
			return nil
		},
	}
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current authentication status",
		RunE: func(cmd *cobra.Command, args []string) error {
			token, err := auth.LoadToken()
			if err != nil {
				fmt.Println("  Not authenticated.")
				fmt.Println("  Run 'ws auth login' to authenticate with GitHub.")
				return nil
			}

			// Fetch username from GitHub API
			req, err := http.NewRequest("GET", "https://api.github.com/user", nil)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+token.AccessToken)
			req.Header.Set("Accept", "application/vnd.github+json")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				fmt.Printf("  Token stored (created %s) but API unreachable\n", token.CreatedAt.Format("2006-01-02"))
				return nil
			}
			defer resp.Body.Close()

			if resp.StatusCode != 200 {
				fmt.Printf("  Token stored but invalid (HTTP %d). Run 'ws auth login' to re-authenticate.\n", resp.StatusCode)
				return nil
			}

			var user struct {
				Login string `json:"login"`
			}
			json.NewDecoder(resp.Body).Decode(&user)

			path, _ := auth.TokenPath()
			fmt.Printf("  Authenticated as: %s\n", user.Login)
			fmt.Printf("  Token: %s\n", path)
			fmt.Printf("  Scopes: %s\n", token.Scope)
			fmt.Printf("  Created: %s\n", token.CreatedAt.Format("2006-01-02 15:04"))
			return nil
		},
	}
}
