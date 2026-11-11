package cmd

import (
	"context"
	"errors"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

const nvkePullRuntimeState = "requires-workload-ownership-scheduling-node-keyring-plugins-registry-availability-fresh-use-and-linked-admission"

func newNVKEAgentInstallPlanPullRuntimeCommand() *cobra.Command {
	group := &cobra.Command{Use: "pull-runtime", Short: "Review retained Pod/node image-pull correspondence"}
	leaf := &cobra.Command{Use: "review OPERATION_GUID", Args: cobra.ExactArgs(1), Short: "Read correspondence between exact prepared analysis and node captures", Long: `Read historical correspondence between retained admitted-Pod analysis and enrolled-node captures.
Use --artifact for the existing Secret analysis and its published authorityDigest.
This read does not prepare captures, retry preparation, authorize native work,
prove registry access or release the original installation uncertainty fence.`, RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
		return runNVKEAgentInstallPlanPullRuntime(cmd, ctx, c, args[0])
	})}
	nvkeAgentJobFlags(leaf)
	for _, name := range []string{"plan", "plan-digest", "chart-artifact", "chart-artifact-digest", "tree", "tree-digest", "workload", "workload-digest", "binding", "binding-digest", "artifact", "analysis-digest", "pull-authority", "pull-authority-digest", "node-platform", "node-platform-digest"} {
		leaf.Flags().String(name, "", "Exact reviewed "+name+" (required)")
		_ = leaf.MarkFlagRequired(name)
	}
	for _, name := range []string{"plan-version", "chart-artifact-version", "tree-version", "workload-version", "binding-version", "pull-authority-version", "analysis-version", "node-platform-version"} {
		leaf.Flags().Int(name, 0, "Exact reviewed "+name+" (required)")
		_ = leaf.MarkFlagRequired(name)
	}
	group.AddCommand(leaf)
	return group
}

func validateNVKEInstallPlanPullRuntime(row *api.NVKEInstallPlanPullRuntime, sources *nvkeReviewedImageBindingSources, parent *api.NVKEInstallPlanPullAuthority, input api.NVKEInstallPlanPullRuntimeReview) error {
	invalid := errors.New("pull correspondence did not confirm the exact historical parent captures and unresolved execution state")
	if row == nil || row.Analysis == nil || row.NodePlatforms == nil {
		return invalid
	}
	if err := validateNVKEInstallPlanPullSecretAnalysis(row.Analysis, sources.plan, sources.publication, sources.tree, sources.workload, sources.binding, parent, input.AnalysisID); err != nil {
		return err
	}
	if err := validateNVKEInstallPlanNodePlatforms(row.NodePlatforms, sources.plan, sources.publication, sources.tree, sources.workload, sources.binding, input.NodePlatformID); err != nil {
		return err
	}
	if row.Analysis.Version != input.ExpectedAnalysisVersion || row.Analysis.AuthorityDigest != input.AnalysisDigest || row.NodePlatforms.Version != input.ExpectedNodePlatformVersion || row.NodePlatforms.PlatformDigest != input.NodePlatformDigest || !nvkeDigestPattern.MatchString(row.CorrespondenceDigest) || row.ContinuationAuthorized == nil || *row.ContinuationAuthorized || row.ExecutionState != nvkePullRuntimeState || row.ImageUseCount == nil || *row.ImageUseCount != *row.Analysis.ImageUseCount || row.NodeCount == nil || *row.NodeCount < 1 || *row.NodeCount > *row.ImageUseCount || *row.NodeCount > *row.NodePlatforms.NodeCount || row.SourceObservedAt != row.Analysis.SourceObservedAt {
		return invalid
	}
	observed, err := time.Parse(time.RFC3339Nano, row.NodeObservedAt)
	bindingTime, bindingErr := time.Parse(time.RFC3339Nano, sources.binding.ObservedAt)
	nodeTime, nodeErr := time.Parse(time.RFC3339Nano, row.NodePlatforms.ObservedAt)
	if err != nil || bindingErr != nil || nodeErr != nil || observed.Before(bindingTime) || observed.After(nodeTime) {
		return invalid
	}
	return nil
}

func runNVKEAgentInstallPlanPullRuntime(cmd *cobra.Command, ctx context.Context, c *api.Client, id string) error {
	seen := map[string]bool{}
	for _, value := range []string{id, resourceStringFlag(cmd, "plan"), resourceStringFlag(cmd, "chart-artifact"), resourceStringFlag(cmd, "tree"), resourceStringFlag(cmd, "workload"), resourceStringFlag(cmd, "binding"), resourceStringFlag(cmd, "artifact"), resourceStringFlag(cmd, "pull-authority"), resourceStringFlag(cmd, "node-platform")} {
		if nvkeGUID(value) != nil || seen[value] {
			return errors.New("review distinct original, analysis, pull-observation and node-capture UUIDs")
		}
		seen[value] = true
	}
	parentVersion, _ := cmd.Flags().GetInt("pull-authority-version")
	analysisVersion, _ := cmd.Flags().GetInt("analysis-version")
	nodeVersion, _ := cmd.Flags().GetInt("node-platform-version")
	parentDigest, analysisDigest, nodeDigest := resourceStringFlag(cmd, "pull-authority-digest"), resourceStringFlag(cmd, "analysis-digest"), resourceStringFlag(cmd, "node-platform-digest")
	if parentVersion < 1 || analysisVersion < 1 || nodeVersion < 1 || !nvkeDigestPattern.MatchString(parentDigest) || !nvkeDigestPattern.MatchString(analysisDigest) || !nvkeDigestPattern.MatchString(nodeDigest) {
		return errors.New("review exact prepared pull, analysis and node versions and artifact digests")
	}
	sources, err := reviewNVKEImageBindingSources(cmd, ctx, c, id)
	if err != nil {
		return err
	}
	parentID := resourceStringFlag(cmd, "pull-authority")
	parent, err := c.ReviewNVKEInstallPlanPullAuthority(ctx, api.NVKEInstallPlanPullAuthorityReview{NVKEInstallPlanImmutableSource: sources.input, ArtifactID: parentID}, false)
	if err != nil {
		return err
	}
	if err = validateNVKEInstallPlanPullAuthority(parent, sources.plan, sources.publication, sources.tree, sources.workload, sources.binding, parentID); err != nil {
		return err
	}
	if parent.Version != parentVersion || parent.AuthorityDigest != parentDigest {
		return errors.New("the reviewed pull observation changed")
	}
	input := api.NVKEInstallPlanPullRuntimeReview{
		NVKEInstallPlanPullSecretAnalysisSource: api.NVKEInstallPlanPullSecretAnalysisSource{NVKEInstallPlanImmutableSource: sources.input, PullAuthorityID: parentID, ExpectedPullAuthorityVersion: parentVersion, PullAuthorityDigest: parentDigest},
		AnalysisID:                              sources.artifactID, ExpectedAnalysisVersion: analysisVersion, AnalysisDigest: analysisDigest,
		NodePlatformID: resourceStringFlag(cmd, "node-platform"), ExpectedNodePlatformVersion: nodeVersion, NodePlatformDigest: nodeDigest,
	}
	row, err := c.ReviewNVKEInstallPlanPullRuntime(ctx, input)
	if err != nil {
		return err
	}
	if err = validateNVKEInstallPlanPullRuntime(row, sources, parent, input); err != nil {
		return err
	}
	return nvkeJSON(cmd, row)
}
