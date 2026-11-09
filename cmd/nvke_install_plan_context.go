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

func newNVKEAgentInstallPlanContextCommand() *cobra.Command {
	group := &cobra.Command{Use: "context", Short: "Prepare and recover a source-bound private render context"}
	for _, verb := range []string{"prepare", "review", "retry-preparation"} {
		action := verb
		leaf := &cobra.Command{Use: verb + " OPERATION_GUID", Args: cobra.ExactArgs(1),
			Short: verb + " a render context for the exact plan and chart publication",
			Long: `Render-context preparation records a metadata-only recovery journal before its
private API request. Review reads retained publication without rebuilding it.
retry-preparation uses the server's retained private intent and original caller/token.
Capture uses the original frozen credential and reviewed Helm helper. Fresh phase,
resource, credential and image verification remain required for native admission.
These commands never replay the original job or authorize native continuation.`,
			RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
				return runNVKEAgentInstallPlanContext(cmd, ctx, c, args[0], action)
			})}
		nvkeAgentJobFlags(leaf)
		leaf.Flags().String("plan", "", "Exact prepared plan UUID (required)")
		leaf.Flags().Int("plan-version", 0, "Exact prepared plan version (required)")
		leaf.Flags().String("plan-digest", "", "Exact prepared plan SHA-256 (required)")
		leaf.Flags().String("chart-artifact", "", "Exact chart publication UUID (required)")
		leaf.Flags().Int("chart-artifact-version", 0, "Exact chart publication version (required)")
		leaf.Flags().String("chart-artifact-digest", "", "Exact chart publication SHA-256 (required)")
		leaf.Flags().String("artifact", "", "Exact new or retained render-context UUID (required)")
		leaf.Flags().String("tree", "", "Exact execution-tree UUID (required)")
		leaf.Flags().Int("tree-version", 0, "Exact reviewed execution-tree version (required)")
		leaf.Flags().String("tree-digest", "", "Exact execution-tree SHA-256 (required)")
		leaf.Flags().String("item", "", "Exact reviewed Helm item name (required)")
		leaf.Flags().Int("ordinal", -1, "Exact original item ordinal (required)")
		for _, flag := range []string{"plan", "plan-version", "plan-digest", "chart-artifact", "chart-artifact-version", "chart-artifact-digest", "artifact", "tree", "tree-version", "tree-digest", "item", "ordinal"} {
			_ = leaf.MarkFlagRequired(flag)
		}
		if verb == "prepare" {
			leaf.Flags().String("file", "", "Private complete context JSON request or - for stdin (required)")
			leaf.Flags().String("request-file", "", "New or matching private metadata recovery journal (required)")
			_ = leaf.MarkFlagRequired("file")
			_ = leaf.MarkFlagRequired("request-file")
		}
		group.AddCommand(leaf)
	}
	return group
}

type nvkeContextJournal struct {
	nvkeChartJournal
	ChartArtifactID      string `json:"chartArtifactId"`
	ChartArtifactVersion int    `json:"chartArtifactVersion"`
	ChartArtifactDigest  string `json:"chartArtifactDigest"`
	TreeID               string `json:"treeId"`
	TreeVersion          int    `json:"treeVersion"`
	TreeDigest           string `json:"treeDigest"`
	Item                 string `json:"item"`
	Ordinal              int    `json:"ordinal"`
}

func decodeNVKEContextStrict(data []byte, target any) error {
	allowed := map[string]bool{
		"format": true, "artifactId": true, "planId": true, "planDigest": true, "maximumBytes": true,
		"chartArtifactId": true, "chartArtifactVersion": true, "chartArtifactDigest": true,
		"kind": true, "server": true, "organizationId": true, "actorId": true, "siteId": true,
		"siteVersion": true, "authorityVersion": true, "operationId": true, "operationVersion": true,
		"inputSha256": true, "planVersion": true,
		"treeId": true, "treeVersion": true, "treeDigest": true, "item": true, "ordinal": true,
		"revision": true, "upgrade": true, "maximumArtifactBytes": true,
		"maximumCalls": true, "maximumIterations": true, "timeoutSeconds": true,
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return errors.New("context metadata must be one JSON object")
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, e := decoder.Token()
		key, ok := token.(string)
		if e != nil || !ok || !allowed[key] || seen[key] {
			return errors.New("duplicate or invalid context metadata fields")
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return errors.New("invalid context metadata value")
		}
	}
	if !json.Valid(data) {
		return errors.New("context metadata must be one JSON object")
	}
	return decodeNVKEStrict(data, target)
}

func nvkeInstallPlanContextIntent(cmd *cobra.Command, c *api.Client, review api.NVKEAgentJobReview, source *api.NVKEOperation, plan *api.NVKEInstallPlan, publication *api.NVKEInstallPlanCharts, tree *api.NVKEInstallPlanTree, artifactID, item string, ordinal int) (string, error) {
	file, journal := resourceStringFlag(cmd, "file"), resourceStringFlag(cmd, "request-file")
	if file == "" || journal == "" || journal == "-" {
		return "", errors.New("use a private context input and a private recovery journal path")
	}
	text, err := readNVKEText(cmd, file)
	if err != nil {
		return "", err
	}
	var body struct {
		Format               int    `json:"format"`
		ArtifactID           string `json:"artifactId"`
		PlanID               string `json:"planId"`
		PlanDigest           string `json:"planDigest"`
		TreeID               string `json:"treeId"`
		TreeDigest           string `json:"treeDigest"`
		Item                 string `json:"item"`
		Revision             string `json:"revision"`
		Upgrade              *bool  `json:"upgrade"`
		MaximumArtifactBytes string `json:"maximumArtifactBytes"`
		MaximumCalls         int    `json:"maximumCalls"`
		MaximumIterations    int    `json:"maximumIterations"`
		TimeoutSeconds       int    `json:"timeoutSeconds"`
		MaximumBytes         string `json:"maximumBytes"`
	}
	if len(text) > 1024*1024 || decodeNVKEContextStrict(text, &body) != nil || body.Format != 1 || body.ArtifactID != artifactID || body.PlanID != plan.ID || body.PlanDigest != plan.Plan.PlanDigest || body.TreeID != tree.ID || body.TreeDigest != tree.TreeDigest || body.Item != item {
		return "", errors.New("review one bounded context body matching the exact artifact, plan, tree and Helm item")
	}
	limit, err := agentDecimal(body.MaximumBytes)
	artifactLimit, artifactErr := agentDecimal(body.MaximumArtifactBytes)
	revision, revisionErr := agentDecimal(body.Revision)
	if err != nil || limit < 1 || limit > 536870912 || artifactErr != nil || artifactLimit < 65536 || artifactLimit > 134217728 || revisionErr != nil || revision < 1 || body.Upgrade == nil || (!*body.Upgrade && revision != 1) || body.MaximumCalls < 1 || body.MaximumCalls > 2048 || body.MaximumIterations < 1 || body.MaximumIterations > 64 || body.TimeoutSeconds < 1 || body.TimeoutSeconds > 300 {
		return "", errors.New("use canonical bounded render limits, explicit upgrade mode and a positive int64 revision")
	}
	saved := nvkeContextJournal{nvkeChartJournal: nvkeChartJournal{nvkePlanJournal: nvkePlanJournal{
		Format: 1, Kind: "nvke-install-plan-context", Server: c.BaseURL(), OrganizationID: c.Org(),
		ActorID: review.ExpectedActorID, SiteID: source.SiteID, SiteVersion: review.ExpectedSiteVersion,
		AuthorityVersion: review.ExpectedAuthorityVersion, OperationID: source.ID,
		OperationVersion: review.ExpectedOperationVersion, PlanID: plan.ID, InputSHA256: nvkeParameterDigest(text),
	}, PlanVersion: plan.Version, PlanDigest: plan.Plan.PlanDigest, ArtifactID: artifactID},
		ChartArtifactID: publication.ID, ChartArtifactVersion: publication.Version, ChartArtifactDigest: publication.ArtifactDigest, TreeID: tree.ID, TreeVersion: tree.Version, TreeDigest: tree.TreeDigest, Item: item, Ordinal: ordinal}
	_, err = privatefile.ReadFile(journal, 16384)
	if errors.Is(err, os.ErrNotExist) {
		data, e := json.MarshalIndent(saved, "", "  ")
		if e != nil {
			return "", errors.New("context recovery metadata unavailable")
		}
		if e = writeNVKEExclusive(journal, append(data, '\n')); e != nil {
			return "", e
		}
	} else if err != nil {
		return "", errors.New("preserve the original private context recovery journal")
	}
	data, err := privatefile.ReadFile(journal, 16384)
	var retained nvkeContextJournal
	if err != nil || decodeNVKEContextStrict(data, &retained) != nil || !reflect.DeepEqual(saved, retained) {
		return "", errors.New("context recovery source or exact private input changed; preserve the original artifact and journal")
	}
	return string(text), nil
}

func validateNVKEInstallPlanContext(row *api.NVKEInstallPlanContext, plan *api.NVKEInstallPlan, publication *api.NVKEInstallPlanCharts, tree *api.NVKEInstallPlanTree, artifactID, item string, ordinal int) error {
	invalid := errors.New("render context did not confirm the reviewed tree and Helm item; preserve its identity and refresh")
	if row == nil || row.ID != artifactID || row.Version < 1 || row.Status != "prepared" || row.PlanID != plan.ID || row.PlanVersion != plan.Version || row.PlanDigest != plan.Plan.PlanDigest || row.ChartArtifactID != publication.ID || row.ChartArtifactVersion != publication.Version || row.ChartArtifactDigest != publication.ArtifactDigest || row.TreeID != tree.ID || row.TreeVersion != tree.Version || row.TreeDigest != tree.TreeDigest || row.OperationID != plan.OperationID || row.OperationVersion != plan.OperationVersion || row.SiteID != plan.SiteID || row.SiteVersion != plan.SiteVersion || row.AuthorityID != plan.AuthorityID || row.AuthorityVersion != plan.AuthorityVersion || row.Item != item || row.Ordinal == nil || *row.Ordinal != ordinal || row.DocumentCount == nil || *row.DocumentCount < 0 || *row.DocumentCount > 10000 || row.LookupCount == nil || *row.LookupCount < 0 || *row.LookupCount > 2048 || row.ContinuationAuthorized == nil || *row.ContinuationAuthorized || row.ExecutionState != "requires-authenticated-phase-verification-images-and-linked-admission" {
		return invalid
	}
	for _, digest := range []string{row.CaptureDigest, row.FrozenChartSHA256, row.ContextDigest} {
		if !nvkeDigestPattern.MatchString(digest) {
			return invalid
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, row.ObservedAt); err != nil {
		return invalid
	}
	return nil
}

func confirmNVKEContextItem(ctx context.Context, c *api.Client, review api.NVKEInstallPlanReview, plan *api.NVKEInstallPlan, item string, ordinal int) error {
	invalid := errors.New("review the exact Helm item and ordinal in the retained plan")
	if ordinal >= plan.ItemCount {
		return invalid
	}
	var after *string
	seen, cursors := map[string]bool{}, map[string]bool{}
	total, previous, matches := -1, -1, 0
	for range 100 {
		page, err := c.NVKEInstallPlanItems(ctx, review, plan.Plan.PlanDigest, after, 100, item)
		if err != nil {
			return err
		}
		if err = validateNVKEPlanItems(page, plan, 100, item, after == nil); err != nil {
			return err
		}
		if total < 0 {
			total = *page.TotalCount
		}
		if total != *page.TotalCount {
			return invalid
		}
		for _, row := range page.Items {
			if seen[row.Name] || *row.Ordinal <= previous {
				return invalid
			}
			seen[row.Name], previous = true, *row.Ordinal
			if row.Name == item {
				if row.Type != "helm" || *row.Ordinal != ordinal {
					return invalid
				}
				matches++
			}
		}
		if len(seen) > total {
			return invalid
		}
		if page.NextCursor == nil {
			if matches != 1 || len(seen) != total {
				return invalid
			}
			return nil
		}
		if cursors[*page.NextCursor] {
			return invalid
		}
		cursors[*page.NextCursor], after = true, page.NextCursor
	}
	return invalid
}

func runNVKEAgentInstallPlanContext(cmd *cobra.Command, ctx context.Context, c *api.Client, id, verb string) error {
	planID, artifactID, digest := resourceStringFlag(cmd, "plan"), resourceStringFlag(cmd, "artifact"), resourceStringFlag(cmd, "plan-digest")
	treeID, treeDigest, item := resourceStringFlag(cmd, "tree"), resourceStringFlag(cmd, "tree-digest"), resourceStringFlag(cmd, "item")
	treeVersion, _ := cmd.Flags().GetInt("tree-version")
	ordinal, _ := cmd.Flags().GetInt("ordinal")
	chartID, chartDigest := resourceStringFlag(cmd, "chart-artifact"), resourceStringFlag(cmd, "chart-artifact-digest")
	version, _ := cmd.Flags().GetInt("plan-version")
	chartVersion, _ := cmd.Flags().GetInt("chart-artifact-version")
	identities := map[string]bool{}
	for _, value := range []string{id, planID, chartID, treeID, artifactID} {
		if nvkeGUID(value) != nil || identities[value] {
			return errors.New("review distinct exact original, plan, chart, tree and context UUIDs")
		}
		identities[value] = true
	}
	if version < 1 || chartVersion < 1 || treeVersion < 1 || ordinal < 0 || ordinal > 9999 || !nvkePlanItemName.MatchString(item) || !nvkeDigestPattern.MatchString(treeDigest) || !nvkeDigestPattern.MatchString(digest) || !nvkeDigestPattern.MatchString(chartDigest) {
		return errors.New("review exact plan and chart publication versions and SHA-256 digests")
	}
	review, source, err := nvkeAgentJobReview(cmd, ctx, c, id)
	if err != nil {
		return err
	}
	if source.Status != "UNCERTAIN" || source.OperationKey != "tooling.management.install" {
		return errors.New("context capture requires the original uncertain management installation")
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
		return errors.New("the prepared plan changed; review before render-context preparation")
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
		return errors.New("the prepared chart publication changed; review before context capture")
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
		return errors.New("the execution tree changed; review before context capture")
	}
	if err = confirmNVKEContextItem(ctx, c, api.NVKEInstallPlanReview{NVKEAgentJobReview: review, PlanID: planID}, plan, item, ordinal); err != nil {
		return err
	}
	contextSource := api.NVKEInstallPlanContextSource{NVKEInstallPlanTreeSource: input, TreeID: treeID, ExpectedTreeVersion: treeVersion, TreeDigest: treeDigest, Item: item, Ordinal: ordinal}
	var row *api.NVKEInstallPlanContext
	if verb == "prepare" {
		var text string
		text, err = nvkeInstallPlanContextIntent(cmd, c, review, source, plan, publication, tree, artifactID, item, ordinal)
		if err != nil {
			return err
		}
		row, err = c.PrepareNVKEInstallPlanContext(ctx, api.NVKEInstallPlanContextPrepare{NVKEInstallPlanContextSource: contextSource, RequestText: text})
	} else {
		row, err = c.ReviewNVKEInstallPlanContext(ctx, api.NVKEInstallPlanContextReview{NVKEInstallPlanContextSource: contextSource, ArtifactID: artifactID}, verb == "retry-preparation")
	}
	if err != nil {
		return fmt.Errorf("%w; retain --artifact %s and exact source/publication versions; use install-plan context review before another preparation", err, artifactID)
	}
	if err = validateNVKEInstallPlanContext(row, plan, publication, tree, artifactID, item, ordinal); err != nil {
		return err
	}
	return nvkeJSON(cmd, row)
}
