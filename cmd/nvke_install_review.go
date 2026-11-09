package cmd

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/nvkecatalog"
	"github.com/spf13/cobra"
)

var nvkeInstallWorkloadName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,252}$`)

func newNVKEAgentInstallReviewCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "install-review OPERATION_GUID", Args: cobra.ExactArgs(1), Short: "Review the original uncertain management install without continuing or clearing guards", Long: `Read current management readiness under reviewed administrator, site, agent and
operation versions. The report is bound to the original native run. Native counters
retain decimal precision and unknown observations remain null. Review does not
authorize a continuation, mark the failed install successful or release its fence.`, RunE: nvkeScoped(runNVKEAgentInstallReview)}
	nvkeAgentJobFlags(cmd)
	return cmd
}

func runNVKEAgentInstallReview(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
	review, source, err := nvkeAgentJobReview(cmd, ctx, c, args[0])
	if err != nil {
		return err
	}
	if source.OperationKey != "tooling.management.install" || source.Status != "UNCERTAIN" {
		return errors.New("review requires the original uncertain management installation")
	}
	selected, err := c.NVKEAgentAuthority(ctx, source.SiteID)
	if err != nil {
		return err
	}
	if err = validateNVKEAgentReply(selected, api.NVKEAgentReview{SiteID: source.SiteID, ExpectedSiteVersion: review.ExpectedSiteVersion, ExpectedVersion: review.ExpectedAuthorityVersion}, "verify"); err != nil {
		return err
	}
	result, err := c.ReviewNVKEAgentInstall(ctx, review)
	if err != nil {
		return err
	}
	if err = validateNVKEInstallReview(result, source, review, selected); err != nil {
		return err
	}
	return nvkeJSON(cmd, result)
}

func validateNVKEInstallReview(row *api.NVKEInstallReview, source *api.NVKEOperation, review api.NVKEAgentJobReview, selected *api.NVKEAgentAuthority) error {
	invalid := errors.New("management install review did not confirm the original source or observations; refresh before further action")
	if row == nil || row.OperationID != source.ID || row.OperationVersion != review.ExpectedOperationVersion || row.SiteID != source.SiteID || row.SiteVersion != review.ExpectedSiteVersion || row.AuthorityID != selected.ID || row.AuthorityVersion != review.ExpectedAuthorityVersion || row.InstanceID != selected.InstanceID || row.BundleManifestSHA256 != selected.BundleManifestSHA256 || row.NativeRevision != nvkecatalog.Pinned().Revision || nvkeGUID(row.NativeRunID) != nil || row.NativeExitCode == 0 || row.NativeExitCode < -255 || row.NativeExitCode > 255 || row.APIReady == nil || !*row.APIReady || row.ContinuationAuthorized == nil || *row.ContinuationAuthorized || len(row.Workloads) == 0 || len(row.Workloads) > 10000 {
		return invalid
	}
	for _, value := range []string{row.ReviewDigest, row.NativeArgumentsSHA256, row.ReviewerSHA256, row.QualificationHelperSHA256, row.Evidence.GuardSHA256, row.Evidence.StartedSHA256, row.Evidence.FinishedSHA256, row.Evidence.StdoutSHA256, row.Evidence.StderrSHA256} {
		if !nvkeDigestPattern.MatchString(value) {
			return invalid
		}
	}
	for _, value := range []string{row.ObservedAt, row.OriginalStartedAt} {
		if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
			return invalid
		}
	}
	return validateNVKEInstallWorkloads(row.Workloads, invalid)
}

func validateNVKEInstallWorkloads(items []api.NVKEInstallWorkload, invalid error) error {
	seen := make(map[[3]string]bool)
	for _, item := range items {
		key := [3]string{item.Namespace, item.Kind, item.Name}
		if seen[key] || !nvkeInstallWorkloadName.MatchString(item.Namespace) || !nvkeInstallWorkloadName.MatchString(item.Name) || (item.Kind != "Deployment" && item.Kind != "StatefulSet") || (item.Observation != "ready" && item.Observation != "requires-review") {
			return invalid
		}
		seen[key] = true
		generation, genErr := agentDecimal(item.Generation)
		desired, desiredErr := agentDecimal(item.DesiredReplicas)
		if genErr != nil || desiredErr != nil {
			return invalid
		}
		for _, value := range []*string{item.ObservedGeneration, item.ReadyReplicas, item.UpdatedReplicas} {
			if value != nil {
				if _, err := agentDecimal(*value); err != nil {
					return invalid
				}
			}
		}
		if item.Observation == "ready" {
			if item.ObservedGeneration == nil || item.ReadyReplicas == nil || item.UpdatedReplicas == nil || desired == 0 {
				return invalid
			}
			observed, _ := agentDecimal(*item.ObservedGeneration)
			if observed < generation || *item.ReadyReplicas != item.DesiredReplicas || *item.UpdatedReplicas != item.DesiredReplicas {
				return invalid
			}
		}
	}
	return nil
}
