package cmd

import (
	"context"
	"errors"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

func newNVKEMaintenanceCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "maintenance OPERATION_GUID", Short: "Read historical certificate-renewal and etcd-rotation observations", Long: `Read typed maintenance metadata from a successful controller get-cluster operation.
The observation includes its source time, scheduling, cycle/workflow identity,
native state and retained mutation-lock evidence. Missing evidence remains null.

The next scheduled renewal check is not certificate expiry. An accepted cycle
does not prove completion, and provisioning success does not clear a retained
maintenance lock. Run a new controller get-cluster operation for a new observation.
This command performs no native I/O and never starts, resets or unlocks native work.`, Args: cobra.ExactArgs(1), RunE: nvkeScoped(runNVKEMaintenance)}
	nvkeReadSiteFlags(cmd)
	cmd.Flags().Int("version", 0, "Exact successful source operation version (required)")
	_ = cmd.MarkFlagRequired("version")
	return cmd
}

func runNVKEMaintenance(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
	site := resourceStringFlag(cmd, "site")
	version, _ := cmd.Flags().GetInt("version")
	if nvkeGUID(site) != nil || nvkeGUID(args[0]) != nil || version < 1 {
		return errors.New("use exact site/operation GUIDs and a positive reviewed operation version")
	}
	_, actor, err := reviewedRequestScope(cmd, c)
	if err != nil {
		return errors.New("NVKE maintenance caller identity unavailable")
	}
	observation, err := c.NVKEMaintenance(ctx, site, args[0], actor, version)
	if err != nil {
		return err
	}
	if err = nvkeGUID(observation.ClusterID); err != nil {
		return errors.New("NVKE maintenance cluster identity unavailable")
	}
	return nvkeJSON(cmd, observation)
}
