package cmd

import (
	"errors"
	"fmt"

	"github.com/calliopeai/astrolift-cli/internal/nvketooling"
	"github.com/spf13/cobra"
)

func newNVKEToolingCommand() *cobra.Command {
	group := &cobra.Command{Use: "tooling", Short: "Prepare and run pinned native NVKE operator tools locally",
		// Site preparation works before a control plane is configured and needs no advisory network lookup.
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error { return nil },
		PersistentPostRun: func(_ *cobra.Command, _ []string) {},
		Long: `Run native bootstrap, site-spec, release-inspection and management-stack tools
on the operator machine. Python 3.12+ and the upstream toolchain are required.
Native output and credentials stay in private workspaces. Process completion is
distinct from infrastructure readiness. Inspect retained state before recovery.`}
	catalog := &cobra.Command{Use: "catalog", Short: "Show the offline runner pin and supported native commands", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		info, err := nvketooling.Info()
		if err != nil {
			return err
		}
		return nvkeJSON(cmd, info)
	}}
	prepare := &cobra.Command{Use: "prepare", Short: "Build a native tool bundle from an exact pinned source archive", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		bundle, err := nvketooling.Prepare(cmd.Context(), resourceStringFlag(cmd, "python"), resourceStringFlag(cmd, "checkout"), resourceStringFlag(cmd, "output"))
		if err != nil {
			return err
		}
		return nvkeJSON(cmd, bundle)
	}}
	prepare.Flags().String("checkout", "", "NVKE checkout containing the pinned commit (required)")
	prepare.Flags().String("output", "", "New private bundle directory (required; never replaced)")
	prepare.Flags().String("python", "", "Python 3.12+ executable (auto-detected by default)")
	_ = prepare.MarkFlagRequired("checkout")
	_ = prepare.MarkFlagRequired("output")
	run := &cobra.Command{Use: "run --bundle DIR --workspace DIR -- NATIVE_COMMAND [FLAGS]", Short: "Execute a native command and return private artifact metadata", Args: func(cmd *cobra.Command, args []string) error {
		if cmd.ArgsLenAtDash() != 0 || len(args) == 0 {
			return errors.New("put the complete native command and flags after --")
		}
		return nil
	}, RunE: func(cmd *cobra.Command, args []string) error {
		result, err := nvketooling.Run(cmd.Context(), resourceStringFlag(cmd, "python"), resourceStringFlag(cmd, "bundle"), resourceStringFlag(cmd, "workspace"), args, cmd.InOrStdin())
		if err != nil {
			return err
		}
		if boolFlag(cmd, "json") {
			if err := nvkeJSON(cmd, result); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  native exit %d\nstdout: %s\nstderr: %s\n", result.RunID, result.Phase, result.ExitCode, result.Stdout, result.Stderr)
		}
		if result.ExitCode != 0 {
			return errors.New("native command did not complete successfully; inspect the private run diagnostics")
		}
		return nil
	}}
	run.Flags().String("bundle", "", "Prepared private native tool bundle (required)")
	run.Flags().String("workspace", "", "Existing owner-only workspace directory (required)")
	run.Flags().String("python", "", "Python 3.12+ executable (auto-detected by default)")
	_ = run.MarkFlagRequired("bundle")
	_ = run.MarkFlagRequired("workspace")
	backend := &cobra.Command{Use: "nktp-backend", Short: "Render source-pinned NKTP TLS backend overrides from Secret references", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result, err := nvketooling.RenderBackend(cmd.Context(), resourceStringFlag(cmd, "python"), resourceStringFlag(cmd, "bundle"), resourceStringFlag(cmd, "profile"), resourceStringFlag(cmd, "output"))
		if err != nil {
			return err
		}
		return nvkeJSON(cmd, result)
	}}
	backend.Flags().String("bundle", "", "Prepared pinned native bundle (required)")
	backend.Flags().String("profile", "", "JSON backend profile with Secret references, never credential values (required)")
	backend.Flags().String("output", "", "New private Helm values file (required; never replaced)")
	backend.Flags().String("python", "", "Python 3.12+ executable (auto-detected by default)")
	for _, flag := range []string{"bundle", "profile", "output"} {
		_ = backend.MarkFlagRequired(flag)
	}
	apis := &cobra.Command{Use: "management-apis", Short: "Render pinned private operator/tenant API manifests", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result, err := nvketooling.RenderManagementAPIs(cmd.Context(), resourceStringFlag(cmd, "python"), resourceStringFlag(cmd, "bundle"), resourceStringFlag(cmd, "profile"), resourceStringFlag(cmd, "output"))
		if err != nil {
			return err
		}
		return nvkeJSON(cmd, result)
	}}
	apis.Flags().String("bundle", "", "Prepared pinned native bundle (required)")
	apis.Flags().String("profile", "", "Private API JSON profile with immutable images and tenant/client scope (required)")
	apis.Flags().String("output", "", "New private Kubernetes manifest (required; never replaced)")
	apis.Flags().String("python", "", "Python 3.12+ with PyYAML (auto-detected by default)")
	for _, flag := range []string{"bundle", "profile", "output"} {
		_ = apis.MarkFlagRequired(flag)
	}
	controller := &cobra.Command{Use: "management-controller", Short: "Render a pinned private tenant-scoped management controller", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result, err := nvketooling.RenderManagementController(cmd.Context(), resourceStringFlag(cmd, "python"), resourceStringFlag(cmd, "bundle"), resourceStringFlag(cmd, "profile"), resourceStringFlag(cmd, "output"))
		if err != nil {
			return err
		}
		return nvkeJSON(cmd, result)
	}}
	controller.Flags().String("bundle", "", "Prepared pinned native bundle (required)")
	controller.Flags().String("profile", "", "Controller JSON profile with immutable image, tenant scope and explicit private controller access (required)")
	controller.Flags().String("output", "", "New private Kubernetes manifest (required; never replaced)")
	controller.Flags().String("python", "", "Python 3.12+ with PyYAML (auto-detected by default)")
	for _, flag := range []string{"bundle", "profile", "output"} {
		_ = controller.MarkFlagRequired(flag)
	}
	group.AddCommand(catalog, prepare, run, backend, apis, controller)
	return group
}
