package cmd

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/astrolift/astrolift-cli/internal/auth"
	"github.com/astrolift/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authentication commands (login, logout, status, refresh)",
}

var authLoginCmd = &cobra.Command{
	Use:   "login [server-slug]",
	Short: "Authenticate against an Astrolift server via browser device flow",
	Long: `Initiates a browser device flow login. Opens the platform's
login URL in your default browser, then polls until you complete
authentication. The resulting credentials are stored at
~/.config/astrolift/credentials/<server>.yaml with mode 0600.

If no server slug is given, uses the current_server from your config.
Use 'astro server add' to register a new server first.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		serverSlug := cfg.CurrentServer
		if len(args) == 1 {
			serverSlug = args[0]
		}
		if serverSlug == "" {
			return errors.New("no server selected; run `astro server add` first or pass a slug")
		}
		entry, ok := cfg.Servers[serverSlug]
		if !ok {
			return fmt.Errorf("server %q not registered; run `astro server add %s <api-url>` first", serverSlug, serverSlug)
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Minute)
		defer cancel()

		fmt.Fprintf(cmd.OutOrStdout(), "Starting login flow against %s...\n", entry.APIURL)
		session, err := auth.StartLogin(ctx, entry.APIURL)
		if err != nil {
			return fmt.Errorf("starting login: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "\nOpen this URL in your browser to complete login:\n  %s\n\n", session.LoginURL)
		_ = openBrowser(session.LoginURL)

		fmt.Fprintln(cmd.OutOrStdout(), "Waiting for authentication...")
		creds, err := auth.PollLoginUntil(ctx, entry.APIURL, session)
		if err != nil {
			return fmt.Errorf("polling login: %w", err)
		}

		if err := config.SaveCredentials(serverSlug, creds); err != nil {
			return fmt.Errorf("saving credentials: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Logged in to %s.\n", entry.APIURL)
		return nil
	},
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout [server-slug]",
	Short: "Remove stored credentials for a server",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		serverSlug := cfg.CurrentServer
		if len(args) == 1 {
			serverSlug = args[0]
		}
		if serverSlug == "" {
			return errors.New("no server selected")
		}
		if err := config.DeleteCredentials(serverSlug); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Logged out of %s.\n", serverSlug)
		return nil
	},
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current authentication status",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if cfg.CurrentServer == "" {
			fmt.Fprintln(cmd.OutOrStdout(), "Not logged in. No current server.")
			return nil
		}
		entry := cfg.Servers[cfg.CurrentServer]
		creds, err := config.LoadCredentials(cfg.CurrentServer)
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Server: %s (%s)\nNot logged in.\n", cfg.CurrentServer, entry.APIURL)
			return nil
		}
		state := "valid"
		if creds.IsExpired(time.Minute) {
			state = "EXPIRED — run `astro auth refresh`"
		}
		fmt.Fprintf(
			cmd.OutOrStdout(),
			"Server:     %s\nAPI URL:    %s\nExpires at: %s\nState:      %s\n",
			cfg.CurrentServer, entry.APIURL,
			creds.ExpiresAt.Format(time.RFC3339), state,
		)
		return nil
	},
}

var authRefreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Refresh the current server's access token",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if cfg.CurrentServer == "" {
			return errors.New("no current server selected")
		}
		entry := cfg.Servers[cfg.CurrentServer]
		creds, err := config.LoadCredentials(cfg.CurrentServer)
		if err != nil {
			return fmt.Errorf("loading credentials: %w", err)
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()
		fresh, err := auth.RefreshCredentials(ctx, entry.APIURL, creds.RefreshToken)
		if err != nil {
			return fmt.Errorf("refreshing: %w", err)
		}
		if err := config.SaveCredentials(cfg.CurrentServer, fresh); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Token refreshed.")
		return nil
	},
}

func init() {
	authCmd.AddCommand(authLoginCmd, authLogoutCmd, authStatusCmd, authRefreshCmd)
	rootCmd.AddCommand(authCmd)
}

// openBrowser tries to open the URL in the user's default browser.
// Failure is non-fatal — the URL is also printed to stdout.
func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}
	args = append(args, url)
	return exec.Command(cmd, args...).Start()
}
