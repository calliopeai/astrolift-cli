package cmd

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/auth"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authentication commands (login, wait, logout, status, refresh)",
}

// Device flow, with a relay path for callers that have no browser.
//
// The flow is inherently two-party: something must open a URL a human
// approves. An unattended agent is not that something, but it can carry the
// URL to its human -- so --no-wait hands back the session and exits instead
// of blocking for fifteen minutes, and `astro auth wait` finishes the job
// once the human is done. With --json the session is a machine-readable
// object, so relaying it does not mean scraping prose.
var (
	loginNoWait    bool
	loginNoBrowser bool
)

var authLoginCmd = &cobra.Command{
	Use:   "login [server-slug]",
	Short: "Authenticate against an Astrolift server via browser device flow",
	Long: `Initiates a browser device flow login. Opens the platform's
login URL in your default browser, then polls until you complete
authentication. The resulting credentials are stored at
~/.config/astrolift/credentials/<server>.yaml with mode 0600.

If no server slug is given, uses the current_server from your config.
Use 'astro server add' to register a new server first.

For an agent or any caller without a browser:

  astro auth login --no-wait --json     start the flow, print the session,
                                        exit -- relay login_url to a human
  astro auth wait --session-id <id>     block until they finish, then store
                                        the credentials

--no-browser starts the flow and waits without trying to launch a browser,
which is what you want on a headless box you are watching yourself.

Unattended alternative that needs no browser at all: mint an API token and
export ASTROLIFT_TOKEN=alft_at_..., which authenticates this CLI and the
MCP gateway alike.`,
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

		asJSON := viper.GetBool("output_json")
		out := cmd.OutOrStdout()

		// The start call is quick; only the human step is slow. Bounding it
		// separately keeps --no-wait from inheriting a 15-minute timeout it
		// never uses.
		startCtx, cancelStart := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancelStart()

		if !asJSON {
			fmt.Fprintf(out, "Starting login flow against %s...\n", entry.APIURL)
		}
		session, err := auth.StartLogin(startCtx, entry.APIURL)
		if err != nil {
			return fmt.Errorf("starting login: %w", err)
		}

		if asJSON {
			// Emitted before any waiting, so a relaying caller has the URL
			// the moment it exists rather than after the flow completes.
			if err := renderJSON(cmd, loginSessionReport{
				Server:       serverSlug,
				APIURL:       entry.APIURL,
				LoginURL:     session.LoginURL,
				SessionID:    session.SessionID,
				ExpiresIn:    session.ExpiresIn,
				PollInterval: session.PollInterval,
				Waiting:      !loginNoWait,
			}); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(out, "\nOpen this URL in your browser to complete login:\n  %s\n\n", session.LoginURL)
		}

		if !loginNoBrowser && !loginNoWait {
			_ = openBrowser(session.LoginURL)
		}

		if loginNoWait {
			if !asJSON {
				fmt.Fprintf(out,
					"Not waiting. Finish in the browser, then run:\n  astro auth wait --session-id %s %s\n",
					session.SessionID, serverSlug,
				)
			}
			return nil
		}

		if !asJSON {
			fmt.Fprintln(out, "Waiting for authentication...")
		}
		return awaitLogin(cmd, serverSlug, entry.APIURL, session)
	},
}

// loginSessionReport is the relay payload: everything a caller needs to hand
// the flow to a human and pick it back up later.
type loginSessionReport struct {
	Server       string `json:"server"`
	APIURL       string `json:"api_url"`
	LoginURL     string `json:"login_url"`
	SessionID    string `json:"session_id"`
	ExpiresIn    int    `json:"expires_in_seconds"`
	PollInterval int    `json:"poll_interval_seconds"`
	// Waiting says whether this process is going to poll to completion. A
	// relaying caller reads it to know whether it must run `auth wait`.
	Waiting bool `json:"waiting"`
}

type loginResultReport struct {
	Server        string `json:"server"`
	APIURL        string `json:"api_url"`
	Authenticated bool   `json:"authenticated"`
	ExpiresAt     string `json:"expires_at"`
}

// awaitLogin polls to completion and stores the credentials. Shared by
// `login` (the waiting case) and `wait` so the two cannot diverge on what
// "logged in" means.
func awaitLogin(cmd *cobra.Command, serverSlug, apiURL string, session *auth.LoginSession) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Minute)
	defer cancel()

	creds, err := auth.PollLoginUntil(ctx, apiURL, session)
	if err != nil {
		return fmt.Errorf("polling login: %w", err)
	}
	if err := config.SaveCredentials(serverSlug, creds); err != nil {
		return fmt.Errorf("saving credentials: %w", err)
	}

	if viper.GetBool("output_json") {
		return renderJSON(cmd, loginResultReport{
			Server:        serverSlug,
			APIURL:        apiURL,
			Authenticated: true,
			ExpiresAt:     creds.ExpiresAt.Format(time.RFC3339),
		})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Logged in to %s.\n", apiURL)
	return nil
}

var (
	waitSessionID    string
	waitPollInterval int
	waitExpiresIn    int
)

var authWaitCmd = &cobra.Command{
	Use:   "wait [server-slug]",
	Short: "Finish a login started with --no-wait",
	Long: `Polls a device-flow session started by 'astro auth login --no-wait'
until the human completes it, then stores the credentials.

The session id comes from that command's output (login_url's sibling
session_id under --json). This exists so an agent can hand a login URL to a
person and pick the flow back up, rather than holding a process open for
fifteen minutes.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(waitSessionID) == "" {
			return errors.New("--session-id is required (take it from `astro auth login --no-wait --json`)")
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		serverSlug := cfg.CurrentServer
		if len(args) == 1 {
			serverSlug = args[0]
		}
		if serverSlug == "" {
			return errors.New("no server selected; pass a slug or set one with `astro server use`")
		}
		entry, ok := cfg.Servers[serverSlug]
		if !ok {
			return fmt.Errorf("server %q not registered", serverSlug)
		}

		// Rebuilt rather than persisted: the id is the only part the server
		// remembers, and writing a half-finished login to disk would leave
		// state nothing cleans up.
		session := &auth.LoginSession{
			SessionID:    strings.TrimSpace(waitSessionID),
			PollInterval: waitPollInterval,
			ExpiresIn:    waitExpiresIn,
		}
		return awaitLogin(cmd, serverSlug, entry.APIURL, session)
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
	authLoginCmd.Flags().BoolVar(&loginNoWait, "no-wait", false,
		"start the flow, report the session, and exit without polling")
	authLoginCmd.Flags().BoolVar(&loginNoBrowser, "no-browser", false,
		"do not try to open a browser")

	authWaitCmd.Flags().StringVar(&waitSessionID, "session-id", "",
		"session id from `astro auth login --no-wait`")
	// Defaults mirror what the server hands back on start, so `wait` works
	// with nothing but a session id.
	authWaitCmd.Flags().IntVar(&waitPollInterval, "poll-interval", 5,
		"seconds between polls")
	authWaitCmd.Flags().IntVar(&waitExpiresIn, "expires-in", 900,
		"seconds to keep polling before giving up")

	authCmd.AddCommand(authLoginCmd, authLogoutCmd, authStatusCmd, authRefreshCmd, authWaitCmd)
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
