package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/spf13/cobra"
)

func newNVKEAgentInstallPlanVerificationCommand() *cobra.Command {
	group := &cobra.Command{Use: "verification", Short: "Prepare and recover a source-bound private render verification"}
	for _, verb := range []string{"prepare", "review", "retry-preparation"} {
		action := verb
		leaf := &cobra.Command{Use: verb + " OPERATION_GUID", Args: cobra.ExactArgs(1),
			Short: verb + " a render verification for the exact plan and chart publication",
			Long: `Render-verification preparation records a metadata-only recovery journal before its
private API request. Review reads retained publication without rebuilding it.
retry-preparation uses the server's retained private intent and original caller/token.
Verification reads with the original frozen credentials. Historical observation
recovery does not renew freshness for phase use. Fresh phase,
resource, credential and image verification remain required for native admission.
These commands never replay the original job or authorize native continuation.`,
			RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
				return runNVKEAgentInstallPlanVerification(cmd, ctx, c, args[0], action)
			})}
		nvkeAgentJobFlags(leaf)
		leaf.Flags().String("plan", "", "Exact prepared plan UUID (required)")
		leaf.Flags().Int("plan-version", 0, "Exact prepared plan version (required)")
		leaf.Flags().String("plan-digest", "", "Exact prepared plan SHA-256 (required)")
		leaf.Flags().String("chart-artifact", "", "Exact chart publication UUID (required)")
		leaf.Flags().Int("chart-artifact-version", 0, "Exact chart publication version (required)")
		leaf.Flags().String("chart-artifact-digest", "", "Exact chart publication SHA-256 (required)")
		leaf.Flags().String("artifact", "", "Exact new or retained render-verification UUID (required)")
		leaf.Flags().String("tree", "", "Exact execution-tree UUID (required)")
		leaf.Flags().Int("tree-version", 0, "Exact reviewed execution-tree version (required)")
		leaf.Flags().String("tree-digest", "", "Exact execution-tree SHA-256 (required)")
		leaf.Flags().String("context", "", "Exact retained render-context UUID (required)")
		leaf.Flags().Int("context-version", 0, "Exact prepared render-context version (required)")
		leaf.Flags().String("context-digest", "", "Exact captured context SHA-256 (required)")
		leaf.Flags().String("item", "", "Exact reviewed Helm item name (required)")
		leaf.Flags().Int("ordinal", -1, "Exact original item ordinal (required)")
		for _, flag := range []string{"plan", "plan-version", "plan-digest", "chart-artifact", "chart-artifact-version", "chart-artifact-digest", "artifact", "tree", "tree-version", "tree-digest", "context", "context-version", "context-digest", "item", "ordinal"} {
			_ = leaf.MarkFlagRequired(flag)
		}
		if verb == "prepare" {
			leaf.Flags().String("file", "", "Private complete verification JSON request or - for stdin (required)")
			leaf.Flags().String("request-file", "", "New or matching private metadata recovery journal (required)")
			_ = leaf.MarkFlagRequired("file")
			_ = leaf.MarkFlagRequired("request-file")
		}
		group.AddCommand(leaf)
	}
	return group
}

type nvkeVerificationJournal struct {
	nvkeContextJournal
	ContextID      string `json:"contextId"`
	ContextVersion int    `json:"contextVersion"`
	ContextDigest  string `json:"contextDigest"`
}

func decodeNVKEVerificationStrict(data []byte, target any) error {
	allowed := map[string]bool{
		"format": true, "artifactId": true, "planId": true, "planDigest": true, "maximumBytes": true,
		"chartArtifactId": true, "chartArtifactVersion": true, "chartArtifactDigest": true,
		"kind": true, "server": true, "organizationId": true, "actorId": true, "siteId": true,
		"siteVersion": true, "authorityVersion": true, "operationId": true, "operationVersion": true,
		"inputSha256": true, "planVersion": true,
		"treeId": true, "treeVersion": true, "treeDigest": true, "item": true, "ordinal": true,
		"contextId": true, "contextVersion": true, "contextDigest": true,
		"maximumCalls": true, "maximumCaptureAgeSeconds": true, "timeoutSeconds": true,
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return errors.New("verification metadata must be one JSON object")
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, e := decoder.Token()
		key, ok := token.(string)
		if e != nil || !ok || !allowed[key] || seen[key] {
			return errors.New("duplicate or invalid verification metadata fields")
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return errors.New("invalid verification metadata value")
		}
	}
	if !json.Valid(data) {
		return errors.New("verification metadata must be one JSON object")
	}
	return decodeNVKEStrict(data, target)
}

func nvkeInstallPlanVerificationIntent(cmd *cobra.Command, c *api.Client, review api.NVKEAgentJobReview, source *api.NVKEOperation, plan *api.NVKEInstallPlan, publication *api.NVKEInstallPlanCharts, tree *api.NVKEInstallPlanTree, captured *api.NVKEInstallPlanContext, artifactID, item string, ordinal int) (string, error) {
	file, journal := resourceStringFlag(cmd, "file"), resourceStringFlag(cmd, "request-file")
	if file == "" || journal == "" || journal == "-" {
		return "", errors.New("use a private verification input and a private recovery journal path")
	}
	text, err := readNVKEText(cmd, file)
	if err != nil {
		return "", err
	}
	var body struct {
		Format                   int    `json:"format"`
		ArtifactID               string `json:"artifactId"`
		PlanID                   string `json:"planId"`
		PlanDigest               string `json:"planDigest"`
		TreeID                   string `json:"treeId"`
		TreeDigest               string `json:"treeDigest"`
		ContextID                string `json:"contextId"`
		ContextDigest            string `json:"contextDigest"`
		Item                     string `json:"item"`
		MaximumBytes             string `json:"maximumBytes"`
		MaximumCaptureAgeSeconds int    `json:"maximumCaptureAgeSeconds"`
		MaximumCalls             int    `json:"maximumCalls"`
		TimeoutSeconds           int    `json:"timeoutSeconds"`
	}
	if len(text) > 1024*1024 || decodeNVKEVerificationStrict(text, &body) != nil || body.Format != 1 || body.ArtifactID != artifactID || body.PlanID != plan.ID || body.PlanDigest != plan.Plan.PlanDigest || body.TreeID != tree.ID || body.TreeDigest != tree.TreeDigest || body.Item != item || body.ContextID != captured.ID || body.ContextDigest != captured.ContextDigest {
		return "", errors.New("review one bounded verification body matching the exact artifact, plan, tree and Helm item")
	}
	limit, err := agentDecimal(body.MaximumBytes)
	if err != nil || limit < 1 || limit > 536870912 || body.MaximumCaptureAgeSeconds < 1 || body.MaximumCaptureAgeSeconds > 3600 || body.MaximumCalls < 1 || body.MaximumCalls > 2048 || body.TimeoutSeconds < 1 || body.TimeoutSeconds > 300 {
		return "", errors.New("use canonical bounded verification bytes, capture age, calls and timeout")
	}

	saved := nvkeVerificationJournal{nvkeContextJournal: nvkeContextJournal{nvkeChartJournal: nvkeChartJournal{nvkePlanJournal: nvkePlanJournal{
		Format: 1, Kind: "nvke-install-plan-verification", Server: c.BaseURL(), OrganizationID: c.Org(),
		ActorID: review.ExpectedActorID, SiteID: source.SiteID, SiteVersion: review.ExpectedSiteVersion,
		AuthorityVersion: review.ExpectedAuthorityVersion, OperationID: source.ID,
		OperationVersion: review.ExpectedOperationVersion, PlanID: plan.ID, InputSHA256: nvkeParameterDigest(text),
	}, PlanVersion: plan.Version, PlanDigest: plan.Plan.PlanDigest, ArtifactID: artifactID},
		ChartArtifactID: publication.ID, ChartArtifactVersion: publication.Version, ChartArtifactDigest: publication.ArtifactDigest, TreeID: tree.ID, TreeVersion: tree.Version, TreeDigest: tree.TreeDigest, Item: item, Ordinal: ordinal}, ContextID: captured.ID, ContextVersion: captured.Version, ContextDigest: captured.ContextDigest}
	_, err = privatefile.ReadFile(journal, 16384)
	if errors.Is(err, os.ErrNotExist) {
		data, e := json.MarshalIndent(saved, "", "  ")
		if e != nil {
			return "", errors.New("verification recovery metadata unavailable")
		}
		if e = writeNVKEExclusive(journal, append(data, '\n')); e != nil {
			return "", e
		}
	} else if err != nil {
		return "", errors.New("preserve the original private verification recovery journal")
	}
	data, err := privatefile.ReadFile(journal, 16384)
	var retained nvkeVerificationJournal
	if err != nil || decodeNVKEVerificationStrict(data, &retained) != nil || !reflect.DeepEqual(saved, retained) {
		return "", errors.New("verification recovery source or exact private input changed; preserve the original artifact and journal")
	}
	return string(text), nil
}

func validateNVKEInstallPlanVerification(row *api.NVKEInstallPlanVerification, plan *api.NVKEInstallPlan, publication *api.NVKEInstallPlanCharts, tree *api.NVKEInstallPlanTree, captured *api.NVKEInstallPlanContext, artifactID, item string, ordinal int) error {
	invalid := errors.New("render verification did not confirm the reviewed tree and Helm item; preserve its identity and refresh")
	if row == nil || row.ID != artifactID || row.Version < 1 || row.Status != "prepared" || row.PlanID != plan.ID || row.PlanVersion != plan.Version || row.PlanDigest != plan.Plan.PlanDigest || row.ChartArtifactID != publication.ID || row.ChartArtifactVersion != publication.Version || row.ChartArtifactDigest != publication.ArtifactDigest || row.TreeID != tree.ID || row.TreeVersion != tree.Version || row.TreeDigest != tree.TreeDigest || row.OperationID != plan.OperationID || row.OperationVersion != plan.OperationVersion || row.SiteID != plan.SiteID || row.SiteVersion != plan.SiteVersion || row.AuthorityID != plan.AuthorityID || row.AuthorityVersion != plan.AuthorityVersion || row.Item != item || row.Ordinal == nil || *row.Ordinal != ordinal || row.ContextID != captured.ID || row.ContextVersion != captured.Version || row.ContextDigest != captured.ContextDigest || row.CaptureDigest != captured.CaptureDigest || row.FrozenChartSHA256 != captured.FrozenChartSHA256 || row.Requests == nil || *row.Requests < 1 || *row.Requests > 2048 || row.ContinuationAuthorized == nil || *row.ContinuationAuthorized || row.ExecutionState != "requires-phase-use-resource-credential-schema-images-and-linked-admission" {
		return invalid
	}
	for _, digest := range []string{row.CaptureDigest, row.FrozenChartSHA256, row.ContextDigest, row.ArtifactDigest, row.VerificationDigest} {
		if !nvkeDigestPattern.MatchString(digest) {
			return invalid
		}
	}
	if size, err := agentDecimal(row.Bytes); err != nil || size < 1 || size > 536870912 {
		return invalid
	}
	if _, err := time.Parse(time.RFC3339Nano, row.ObservedAt); err != nil {
		return invalid
	}
	return nil
}

func runNVKEAgentInstallPlanVerification(cmd *cobra.Command, ctx context.Context, c *api.Client, id, verb string) error {
	contextID, contextDigest := resourceStringFlag(cmd, "context"), resourceStringFlag(cmd, "context-digest")
	contextVersion, _ := cmd.Flags().GetInt("context-version")
	planID, artifactID, digest := resourceStringFlag(cmd, "plan"), resourceStringFlag(cmd, "artifact"), resourceStringFlag(cmd, "plan-digest")
	treeID, treeDigest, item := resourceStringFlag(cmd, "tree"), resourceStringFlag(cmd, "tree-digest"), resourceStringFlag(cmd, "item")
	treeVersion, _ := cmd.Flags().GetInt("tree-version")
	ordinal, _ := cmd.Flags().GetInt("ordinal")
	chartID, chartDigest := resourceStringFlag(cmd, "chart-artifact"), resourceStringFlag(cmd, "chart-artifact-digest")
	version, _ := cmd.Flags().GetInt("plan-version")
	chartVersion, _ := cmd.Flags().GetInt("chart-artifact-version")
	identities := map[string]bool{}
	for _, value := range []string{id, planID, chartID, treeID, contextID, artifactID} {
		if nvkeGUID(value) != nil || identities[value] {
			return errors.New("review distinct exact original, plan, chart, tree, context and verification UUIDs")
		}
		identities[value] = true
	}
	if version < 1 || chartVersion < 1 || treeVersion < 1 || contextVersion < 1 || !nvkeDigestPattern.MatchString(contextDigest) || ordinal < 0 || ordinal > 9999 || !nvkePlanItemName.MatchString(item) || !nvkeDigestPattern.MatchString(treeDigest) || !nvkeDigestPattern.MatchString(digest) || !nvkeDigestPattern.MatchString(chartDigest) {
		return errors.New("review exact plan and chart publication versions and SHA-256 digests")
	}
	review, source, err := nvkeAgentJobReview(cmd, ctx, c, id)
	if err != nil {
		return err
	}
	if source.Status != "UNCERTAIN" || source.OperationKey != "tooling.management.install" {
		return errors.New("render verification requires the original uncertain management installation")
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
		return errors.New("the prepared plan changed; review before render-verification preparation")
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
		return errors.New("the prepared chart publication changed; review before render verification")
	}
	input := api.NVKEInstallPlanTreeSource{NVKEInstallPlanChartsSource: parent, ChartArtifactID: chartID, ExpectedChartArtifactVersion: chartVersion, ChartArtifactDigest: chartDigest}
	tree, err := c.ReviewNVKEInstallPlanTree(ctx, api.NVKEInstallPlanTreeReview{NVKEInstallPlanTreeSource: input, ArtifactID: treeID}, false)
	if err != nil {
		return err
	}
	if err = validateNVKEInstallPlanTree(tree, plan, publication, treeID, 0); err != nil {
		return err
	}
	if tree.Version != treeVersion || tree.TreeDigest != treeDigest {
		return errors.New("the execution tree changed; review before render verification")
	}
	if err = confirmNVKEContextItem(ctx, c, api.NVKEInstallPlanReview{NVKEAgentJobReview: review, PlanID: planID}, plan, item, ordinal); err != nil {
		return err
	}
	contextSource := api.NVKEInstallPlanContextSource{NVKEInstallPlanTreeSource: input, TreeID: treeID, ExpectedTreeVersion: treeVersion, TreeDigest: treeDigest, Item: item, Ordinal: ordinal}
	captured, err := c.ReviewNVKEInstallPlanContext(ctx, api.NVKEInstallPlanContextReview{NVKEInstallPlanContextSource: contextSource, ArtifactID: contextID}, false)
	if err != nil {
		return err
	}
	if err = validateNVKEInstallPlanContext(captured, plan, publication, tree, contextID, item, ordinal); err != nil {
		return err
	}
	if captured.Version != contextVersion || captured.ContextDigest != contextDigest {
		return errors.New("the captured context changed; review before verification")
	}
	verificationSource := api.NVKEInstallPlanVerificationSource{NVKEInstallPlanContextSource: contextSource, ContextID: contextID, ExpectedContextVersion: contextVersion, ContextDigest: contextDigest}
	var limits struct {
		MaximumBytes string `json:"maximumBytes"`
		MaximumCalls int    `json:"maximumCalls"`
	}
	var row *api.NVKEInstallPlanVerification
	if verb == "prepare" {
		var text string
		text, err = nvkeInstallPlanVerificationIntent(cmd, c, review, source, plan, publication, tree, captured, artifactID, item, ordinal)
		if err != nil {
			return err
		}
		if json.Unmarshal([]byte(text), &limits) != nil {
			return errors.New("verification limits unavailable")
		}
		row, err = c.PrepareNVKEInstallPlanVerification(ctx, api.NVKEInstallPlanVerificationPrepare{NVKEInstallPlanVerificationSource: verificationSource, RequestText: text})
	} else {
		row, err = c.ReviewNVKEInstallPlanVerification(ctx, api.NVKEInstallPlanVerificationReview{NVKEInstallPlanVerificationSource: verificationSource, ArtifactID: artifactID}, verb == "retry-preparation")
	}
	if err != nil {
		return fmt.Errorf("%w; retain --artifact %s and exact source/publication versions; use install-plan verification review before another preparation", err, artifactID)
	}
	if err = validateNVKEInstallPlanVerification(row, plan, publication, tree, captured, artifactID, item, ordinal); err != nil {
		return err
	}
	if verb == "prepare" {
		size, _ := agentDecimal(row.Bytes)
		maximum, _ := agentDecimal(limits.MaximumBytes)
		if size > maximum || *row.Requests > limits.MaximumCalls {
			return errors.New("verification response exceeded reviewed limits")
		}
	}
	return nvkeJSON(cmd, row)
}
