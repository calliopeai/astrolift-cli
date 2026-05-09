package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/astrolift/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage Astrolift servers (add, list, use, remove)",
	Long: `An Astrolift install is identified by its DNS zone (per the
platform's install topology). The CLI tracks one or more servers
in ~/.config/astrolift/config.yaml and switches between them
with 'astro server use <slug>'.`,
}

var serverAddCmd = &cobra.Command{
	Use:   "add <slug> <api-url>",
	Short: "Register a new Astrolift server",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug := args[0]
		apiURL := strings.TrimSuffix(args[1], "/")

		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if cfg.Servers == nil {
			cfg.Servers = map[string]config.ServerEntry{}
		}
		if _, exists := cfg.Servers[slug]; exists {
			return fmt.Errorf("server %q already registered", slug)
		}
		cfg.Servers[slug] = config.ServerEntry{
			APIURL:      apiURL,
			DisplayName: slug,
		}
		// Auto-set as current if it's the first server
		if cfg.CurrentServer == "" {
			cfg.CurrentServer = slug
		}
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Added server %s -> %s\n", slug, apiURL)
		if cfg.CurrentServer == slug {
			fmt.Fprintf(cmd.OutOrStdout(), "Set as current server. Run `astro auth login` to authenticate.\n")
		}
		return nil
	},
}

var serverListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered servers",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if len(cfg.Servers) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No servers registered. Run `astro server add <slug> <api-url>`.")
			return nil
		}
		slugs := make([]string, 0, len(cfg.Servers))
		for slug := range cfg.Servers {
			slugs = append(slugs, slug)
		}
		sort.Strings(slugs)

		fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-40s %s\n", "SLUG", "API URL", "CURRENT")
		for _, slug := range slugs {
			entry := cfg.Servers[slug]
			marker := ""
			if slug == cfg.CurrentServer {
				marker = "*"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-40s %s\n", slug, entry.APIURL, marker)
		}
		return nil
	},
}

var serverUseCmd = &cobra.Command{
	Use:   "use <slug>",
	Short: "Switch the current active server",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug := args[0]
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if _, ok := cfg.Servers[slug]; !ok {
			return fmt.Errorf("server %q not registered", slug)
		}
		cfg.CurrentServer = slug
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Switched to %s\n", slug)
		return nil
	},
}

var serverRemoveCmd = &cobra.Command{
	Use:   "remove <slug>",
	Short: "Remove a server (and its stored credentials)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug := args[0]
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if _, ok := cfg.Servers[slug]; !ok {
			return fmt.Errorf("server %q not registered", slug)
		}
		delete(cfg.Servers, slug)
		if cfg.CurrentServer == slug {
			cfg.CurrentServer = ""
			for next := range cfg.Servers {
				cfg.CurrentServer = next
				break
			}
		}
		_ = config.DeleteCredentials(slug)
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Removed %s\n", slug)
		return nil
	},
}

func init() {
	serverCmd.AddCommand(serverAddCmd, serverListCmd, serverUseCmd, serverRemoveCmd)
	rootCmd.AddCommand(serverCmd)
}
