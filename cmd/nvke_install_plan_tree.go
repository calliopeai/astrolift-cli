package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/spf13/cobra"
)

func newNVKEAgentInstallPlanTreeCommand() *cobra.Command {
	group := &cobra.Command{Use: "tree", Short: "Prepare and recover a source-bound private execution tree"}
	for _, verb := range []string{"prepare", "review", "retry-preparation"} {
		action := verb
		leaf := &cobra.Command{Use: verb + " OPERATION_GUID", Args: cobra.ExactArgs(1),
			Short: verb + " an execution tree for the exact plan and chart publication",
			Long: `Execution-tree preparation records a metadata-only recovery journal before its
private API request. Review reads retained publication without rebuilding it.
retry-preparation uses the server's retained private intent and original caller/token.
Native rendering, image verification and fresh skipped-resource proof remain required.
These commands never replay the original job or authorize native continuation.`,
			RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
				return runNVKEAgentInstallPlanTree(cmd, ctx, c, args[0], action)
			})}
		nvkeAgentJobFlags(leaf)
		leaf.Flags().String("plan", "", "Exact prepared plan UUID (required)")
		leaf.Flags().Int("plan-version", 0, "Exact prepared plan version (required)")
		leaf.Flags().String("plan-digest", "", "Exact prepared plan SHA-256 (required)")
		leaf.Flags().String("chart-artifact", "", "Exact chart publication UUID (required)")
		leaf.Flags().Int("chart-artifact-version", 0, "Exact chart publication version (required)")
		leaf.Flags().String("chart-artifact-digest", "", "Exact chart publication SHA-256 (required)")
		leaf.Flags().String("artifact", "", "Exact new or retained execution-tree UUID (required)")
		for _, flag := range []string{"plan", "plan-version", "plan-digest", "chart-artifact", "chart-artifact-version", "chart-artifact-digest", "artifact"} {
			_ = leaf.MarkFlagRequired(flag)
		}
		if verb == "prepare" {
			leaf.Flags().String("file", "", "Private complete tree JSON request or - for stdin (required)")
			leaf.Flags().String("request-file", "", "New or matching private metadata recovery journal (required)")
			_ = leaf.MarkFlagRequired("file")
			_ = leaf.MarkFlagRequired("request-file")
		}
		group.AddCommand(leaf)
	}
	return group
}

type nvkeTreeJournal struct {
	nvkeChartJournal
	ChartArtifactID      string `json:"chartArtifactId"`
	ChartArtifactVersion int    `json:"chartArtifactVersion"`
	ChartArtifactDigest  string `json:"chartArtifactDigest"`
}

func decodeNVKETreeStrict(data []byte, target any) error {
	allowed := map[string]bool{
		"format": true, "artifactId": true, "planId": true, "planDigest": true, "maximumBytes": true,
		"chartArtifactId": true, "chartArtifactVersion": true, "chartArtifactDigest": true,
		"kind": true, "server": true, "organizationId": true, "actorId": true, "siteId": true,
		"siteVersion": true, "authorityVersion": true, "operationId": true, "operationVersion": true,
		"inputSha256": true, "planVersion": true,
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return errors.New("tree metadata must be one JSON object")
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, e := decoder.Token()
		key, ok := token.(string)
		if e != nil || !ok || !allowed[key] || seen[key] {
			return errors.New("duplicate or invalid tree metadata fields")
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return errors.New("invalid tree metadata value")
		}
	}
	if !json.Valid(data) {
		return errors.New("tree metadata must be one JSON object")
	}
	return decodeNVKEStrict(data, target)
}

func nvkeInstallPlanTreeIntent(cmd *cobra.Command, c *api.Client, review api.NVKEAgentJobReview, source *api.NVKEOperation, plan *api.NVKEInstallPlan, publication *api.NVKEInstallPlanCharts, artifactID string) (string, int64, error) {
	file, journal := resourceStringFlag(cmd, "file"), resourceStringFlag(cmd, "request-file")
	if file == "" || journal == "" || journal == "-" {
		return "", 0, errors.New("use a private tree input and a private recovery journal path")
	}
	text, err := readNVKEText(cmd, file)
	if err != nil {
		return "", 0, err
	}
	var body struct {
		Format              int    `json:"format"`
		ArtifactID          string `json:"artifactId"`
		PlanID              string `json:"planId"`
		PlanDigest          string `json:"planDigest"`
		ChartArtifactID     string `json:"chartArtifactId"`
		ChartArtifactDigest string `json:"chartArtifactDigest"`
		MaximumBytes        string `json:"maximumBytes"`
	}
	if len(text) > 1024*1024 || decodeNVKETreeStrict(text, &body) != nil || body.Format != 1 || body.ArtifactID != artifactID || body.PlanID != plan.ID || body.PlanDigest != plan.Plan.PlanDigest || body.ChartArtifactID != publication.ID || body.ChartArtifactDigest != publication.ArtifactDigest {
		return "", 0, errors.New("review one bounded tree body matching the exact artifact, plan and chart publication")
	}
	limit, err := agentDecimal(body.MaximumBytes)
	if err != nil || limit == 0 {
		return "", 0, errors.New("use a positive decimal int64 execution-tree byte budget")
	}
	saved := nvkeTreeJournal{nvkeChartJournal: nvkeChartJournal{nvkePlanJournal: nvkePlanJournal{
		Format: 1, Kind: "nvke-install-plan-tree", Server: c.BaseURL(), OrganizationID: c.Org(),
		ActorID: review.ExpectedActorID, SiteID: source.SiteID, SiteVersion: review.ExpectedSiteVersion,
		AuthorityVersion: review.ExpectedAuthorityVersion, OperationID: source.ID,
		OperationVersion: review.ExpectedOperationVersion, PlanID: plan.ID, InputSHA256: nvkeParameterDigest(text),
	}, PlanVersion: plan.Version, PlanDigest: plan.Plan.PlanDigest, ArtifactID: artifactID},
		ChartArtifactID: publication.ID, ChartArtifactVersion: publication.Version, ChartArtifactDigest: publication.ArtifactDigest}
	_, err = privatefile.ReadFile(journal, 16384)
	if errors.Is(err, os.ErrNotExist) {
		data, e := json.MarshalIndent(saved, "", "  ")
		if e != nil {
			return "", 0, errors.New("tree recovery metadata unavailable")
		}
		if e = writeNVKEExclusive(journal, append(data, '\n')); e != nil {
			return "", 0, e
		}
	} else if err != nil {
		return "", 0, errors.New("preserve the original private tree recovery journal")
	}
	data, err := privatefile.ReadFile(journal, 16384)
	var retained nvkeTreeJournal
	if err != nil || decodeNVKETreeStrict(data, &retained) != nil || !reflect.DeepEqual(saved, retained) {
		return "", 0, errors.New("tree recovery source or exact private input changed; preserve the original artifact and journal")
	}
	return string(text), limit, nil
}

func validateNVKEInstallPlanTree(row *api.NVKEInstallPlanTree, plan *api.NVKEInstallPlan, publication *api.NVKEInstallPlanCharts, artifactID string, maximumBytes int64) error {
	invalid := errors.New("execution tree did not confirm the exact reviewed source/publications; preserve its identity and refresh")
	if row == nil || row.ID != artifactID || row.Version < 1 || row.Status != "prepared" || row.PlanID != plan.ID || row.PlanVersion != plan.Version || row.PlanDigest != plan.Plan.PlanDigest || row.ChartArtifactID != publication.ID || row.ChartArtifactVersion != publication.Version || row.ChartArtifactDigest != publication.ArtifactDigest || row.OperationID != plan.OperationID || row.OperationVersion != plan.OperationVersion || row.SiteID != plan.SiteID || row.SiteVersion != plan.SiteVersion || row.AuthorityID != plan.AuthorityID || row.AuthorityVersion != plan.AuthorityVersion || !nvkeDigestPattern.MatchString(row.TreeDigest) || row.FileCount == nil || *row.FileCount < 1 || *row.FileCount > 100000 || publication.ChartCount == nil || *row.FileCount < *publication.ChartCount || row.ContinuationAuthorized == nil || *row.ContinuationAuthorized || row.ExecutionState != "requires-native-rendering-images-and-live-verification" {
		return invalid
	}
	size, e1 := agentDecimal(row.Bytes)
	chartBytes, e2 := agentDecimal(publication.Bytes)
	if e1 != nil || e2 != nil || size == 0 || size < chartBytes || (maximumBytes > 0 && size > maximumBytes) {
		return invalid
	}
	return nil
}

func runNVKEAgentInstallPlanTree(cmd *cobra.Command, ctx context.Context, c *api.Client, id, verb string) error {
	planID, artifactID, digest := resourceStringFlag(cmd, "plan"), resourceStringFlag(cmd, "artifact"), resourceStringFlag(cmd, "plan-digest")
	chartID, chartDigest := resourceStringFlag(cmd, "chart-artifact"), resourceStringFlag(cmd, "chart-artifact-digest")
	version, _ := cmd.Flags().GetInt("plan-version")
	chartVersion, _ := cmd.Flags().GetInt("chart-artifact-version")
	identities := map[string]bool{}
	for _, value := range []string{id, planID, chartID, artifactID} {
		if nvkeGUID(value) != nil || identities[value] {
			return errors.New("review distinct exact original, plan, chart and tree UUIDs")
		}
		identities[value] = true
	}
	if version < 1 || chartVersion < 1 || !nvkeDigestPattern.MatchString(digest) || !nvkeDigestPattern.MatchString(chartDigest) {
		return errors.New("review exact plan and chart publication versions and SHA-256 digests")
	}
	review, source, err := nvkeAgentJobReview(cmd, ctx, c, id)
	if err != nil {
		return err
	}
	if source.Status != "UNCERTAIN" || source.OperationKey != "tooling.management.install" {
		return errors.New("tree preparation requires the original uncertain management installation")
	}
	selected, err := c.NVKEAgentAuthority(ctx, source.SiteID)
	if err != nil {
		return err
	}
	if err = validateNVKEAgentReply(selected, api.NVKEAgentReview{SiteID: source.SiteID, ExpectedSiteVersion: review.ExpectedSiteVersion, ExpectedVersion: review.ExpectedAuthorityVersion}, "verify"); err != nil {
		return err
	}
	plan, err := c.ReviewNVKEInstallPlan(ctx, api.NVKEInstallPlanReview{NVKEAgentJobReview: review, PlanID: planID}, false)
	if err != nil {
		return err
	}
	if err = validateNVKEInstallPlan(plan, source, review, selected, planID); err != nil {
		return err
	}
	if plan.Version != version || plan.Plan.PlanDigest != digest {
		return errors.New("the prepared plan changed; review before execution-tree preparation")
	}
	parent := api.NVKEInstallPlanChartsSource{NVKEInstallPlanReview: api.NVKEInstallPlanReview{NVKEAgentJobReview: review, PlanID: planID}, ExpectedPlanVersion: version, PlanDigest: digest}
	publication, err := c.ReviewNVKEInstallPlanCharts(ctx, api.NVKEInstallPlanChartsReview{NVKEInstallPlanChartsSource: parent, ArtifactID: chartID}, false)
	if err != nil {
		return err
	}
	if err = validateNVKEInstallPlanCharts(publication, plan, chartID, nvkeChartLimits{}); err != nil {
		return err
	}
	if publication.Version != chartVersion || publication.ArtifactDigest != chartDigest {
		return errors.New("the prepared chart publication changed; review before tree preparation")
	}
	input := api.NVKEInstallPlanTreeSource{NVKEInstallPlanChartsSource: parent, ChartArtifactID: chartID, ExpectedChartArtifactVersion: chartVersion, ChartArtifactDigest: chartDigest}
	var row *api.NVKEInstallPlanTree
	var limit int64
	if verb == "prepare" {
		var text string
		text, limit, err = nvkeInstallPlanTreeIntent(cmd, c, review, source, plan, publication, artifactID)
		if err != nil {
			return err
		}
		row, err = c.PrepareNVKEInstallPlanTree(ctx, api.NVKEInstallPlanTreePrepare{NVKEInstallPlanTreeSource: input, RequestText: text})
	} else {
		row, err = c.ReviewNVKEInstallPlanTree(ctx, api.NVKEInstallPlanTreeReview{NVKEInstallPlanTreeSource: input, ArtifactID: artifactID}, verb == "retry-preparation")
	}
	if err != nil {
		return fmt.Errorf("%w; retain --artifact %s and exact source/publication versions; use install-plan tree review before another preparation", err, artifactID)
	}
	if err = validateNVKEInstallPlanTree(row, plan, publication, artifactID, limit); err != nil {
		return err
	}
	return nvkeJSON(cmd, row)
}
