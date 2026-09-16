package cmd

import (
	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

// Task GUIDs and workload slugs still require the selected tenant on every request.
func loadScopedAgentClient(cmd *cobra.Command) (*api.Client, *config.Config, error) {
	client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
	if err != nil {
		return nil, nil, err
	}
	if _, err := resolveOrg(cmd, cmd.Context(), client, cfg); err != nil {
		return nil, nil, err
	}
	return client, cfg, nil
}
