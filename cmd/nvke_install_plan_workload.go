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

func newNVKEAgentInstallPlanWorkloadCommand() *cobra.Command {
	group := &cobra.Command{Use: "workload", Short: "Prepare and recover a source-bound all-item workload inventory"}
	for _, verb := range []string{"prepare", "review", "retry-preparation"} {
		action := verb
		leaf := &cobra.Command{Use: verb + " OPERATION_GUID", Args: cobra.ExactArgs(1),
			Short: verb + " a workload inventory for the exact plan and chart publication",
			Long: `Workload inventory preparation seals a private recovery journal before the API request.
Review reads retained workload evidence. Explicit retry uses the server's retained
private intent and its original caller/token. Workload, platform, credential and
fresh use-time evidence remain required for native admission.`,
			RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
				return runNVKEAgentInstallPlanWorkload(cmd, ctx, c, args[0], action)
			})}
		nvkeAgentJobFlags(leaf)
		leaf.Flags().String("plan", "", "Exact prepared plan UUID (required)")
		leaf.Flags().Int("plan-version", 0, "Exact prepared plan version (required)")
		leaf.Flags().String("plan-digest", "", "Exact prepared plan SHA-256 (required)")
		leaf.Flags().String("chart-artifact", "", "Exact chart publication UUID (required)")
		leaf.Flags().Int("chart-artifact-version", 0, "Exact chart publication version (required)")
		leaf.Flags().String("chart-artifact-digest", "", "Exact chart publication SHA-256 (required)")
		leaf.Flags().String("artifact", "", "Exact new or retained workload UUID (required)")
		leaf.Flags().String("tree", "", "Exact execution-tree UUID (required)")
		leaf.Flags().Int("tree-version", 0, "Exact reviewed execution-tree version (required)")
		leaf.Flags().String("tree-digest", "", "Exact execution-tree SHA-256 (required)")
		for _, flag := range []string{"plan", "plan-version", "plan-digest", "chart-artifact", "chart-artifact-version", "chart-artifact-digest", "artifact", "tree", "tree-version", "tree-digest"} {
			_ = leaf.MarkFlagRequired(flag)
		}
		if verb == "prepare" {
			leaf.Flags().String("file", "", "Private complete workload JSON request or - for stdin (required)")
			leaf.Flags().String("request-file", "", "New or matching private metadata recovery journal (required)")
			_ = leaf.MarkFlagRequired("file")
			_ = leaf.MarkFlagRequired("request-file")
		}
		group.AddCommand(leaf)
	}
	return group
}

type nvkeWorkloadJournal struct {
	SourceKind string `json:"sourceKind,omitempty"`
	nvkeChartJournal
	ChartArtifactID      string `json:"chartArtifactId"`
	ChartArtifactVersion int    `json:"chartArtifactVersion"`
	ChartArtifactDigest  string `json:"chartArtifactDigest"`
	TreeID               string `json:"treeId"`
	TreeVersion          int    `json:"treeVersion"`
	TreeDigest           string `json:"treeDigest"`
}

func decodeNVKEWorkloadStrict(data []byte, target any) error {
	allowed := map[string]bool{
		"format": true, "artifactId": true, "planId": true, "planDigest": true, "maximumBytes": true,
		"contextBindings": true, "maximumResources": true, "timeoutSeconds": true,
		"chartArtifactId": true, "chartArtifactVersion": true, "chartArtifactDigest": true,
		"kind": true, "server": true, "organizationId": true, "actorId": true, "siteId": true,
		"siteVersion": true, "authorityVersion": true, "operationId": true, "operationVersion": true,
		"inputSha256": true, "planVersion": true, "treeId": true, "treeVersion": true, "treeDigest": true,
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return errors.New("workload metadata must be one JSON object")
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, e := decoder.Token()
		key, ok := token.(string)
		if e != nil || !ok || !allowed[key] || seen[key] {
			return errors.New("duplicate or invalid workload metadata fields")
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return errors.New("invalid workload metadata value")
		}
	}
	if !json.Valid(data) {
		return errors.New("workload metadata must be one JSON object")
	}
	return decodeNVKEStrict(data, target)
}

func nvkeInstallPlanWorkloadIntent(cmd *cobra.Command, c *api.Client, review api.NVKEAgentJobReview, source *api.NVKEOperation, plan *api.NVKEInstallPlan, publication *api.NVKEInstallPlanCharts, tree *api.NVKEInstallPlanTree, artifactID string) (string, error) {
	file, journal := resourceStringFlag(cmd, "file"), resourceStringFlag(cmd, "request-file")
	if file == "" || journal == "" || journal == "-" {
		return "", errors.New("use a private workload input and a private recovery journal path")
	}
	text, err := readNVKEText(cmd, file)
	if err != nil {
		return "", err
	}
	var body struct {
		Format          int    `json:"format"`
		ArtifactID      string `json:"artifactId"`
		PlanID          string `json:"planId"`
		PlanDigest      string `json:"planDigest"`
		TreeID          string `json:"treeId"`
		TreeDigest      string `json:"treeDigest"`
		ContextBindings map[string]struct {
			ContextID     string `json:"contextId"`
			ContextDigest string `json:"contextDigest"`
		} `json:"contextBindings"`
		MaximumBytes     string `json:"maximumBytes"`
		MaximumResources int    `json:"maximumResources"`
		TimeoutSeconds   int    `json:"timeoutSeconds"`
	}
	if len(text) > 1024*1024 || rejectNVKEWorkloadDuplicateKeys(text) != nil || decodeNVKEWorkloadStrict(text, &body) != nil || body.Format != 1 || body.ArtifactID != artifactID || body.PlanID != plan.ID || body.PlanDigest != plan.Plan.PlanDigest || body.TreeID != tree.ID || body.TreeDigest != tree.TreeDigest {
		return "", errors.New("review a closed workload inventory request matching the exact artifact, plan and tree")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(text, &fields) != nil || len(fields) != 10 {
		return "", errors.New("invalid closed workload inventory request")
	}
	for _, key := range []string{"format", "artifactId", "planId", "planDigest", "treeId", "treeDigest", "contextBindings", "maximumBytes", "maximumResources", "timeoutSeconds"} {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return "", errors.New("missing workload inventory request field")
		}
	}
	var rawBindings map[string]map[string]json.RawMessage
	if json.Unmarshal(fields["contextBindings"], &rawBindings) != nil {
		return "", errors.New("invalid workload context bindings")
	}
	for _, row := range rawBindings {
		if len(row) != 2 || row["contextId"] == nil || row["contextDigest"] == nil || bytes.Equal(bytes.TrimSpace(row["contextId"]), []byte("null")) || bytes.Equal(bytes.TrimSpace(row["contextDigest"]), []byte("null")) {
			return "", errors.New("use closed context UUID and digest bindings")
		}
	}
	ids := map[string]bool{source.ID: true, plan.ID: true, publication.ID: true, tree.ID: true, artifactID: true}
	if len(body.ContextBindings) > 10000 {
		return "", errors.New("too many workload context bindings")
	}
	for name, binding := range body.ContextBindings {
		if !nvkePlanItemName.MatchString(name) || nvkeGUID(binding.ContextID) != nil || ids[binding.ContextID] || !nvkeDigestPattern.MatchString(binding.ContextDigest) {
			return "", errors.New("use distinct reviewed context UUIDs and exact context digests")
		}
		ids[binding.ContextID] = true
	}
	limit, err := agentDecimal(body.MaximumBytes)
	if err != nil || limit < 1 || limit > 64*1024*1024 || body.MaximumResources < 1 || body.MaximumResources > 10000 || body.TimeoutSeconds < 1 || body.TimeoutSeconds > 300 {
		return "", errors.New("use bounded workload bytes, resources and timeout")
	}

	saved := nvkeWorkloadJournal{nvkeChartJournal: nvkeChartJournal{nvkePlanJournal: nvkePlanJournal{
		Format: 1, Kind: "nvke-install-plan-workload", Server: c.BaseURL(), OrganizationID: c.Org(),
		ActorID: review.ExpectedActorID, SiteID: source.SiteID, SiteVersion: review.ExpectedSiteVersion,
		AuthorityVersion: review.ExpectedAuthorityVersion, OperationID: source.ID,
		OperationVersion: review.ExpectedOperationVersion, PlanID: plan.ID, InputSHA256: nvkeParameterDigest(text),
	}, PlanVersion: plan.Version, PlanDigest: plan.Plan.PlanDigest, ArtifactID: artifactID},
		ChartArtifactID: publication.ID, ChartArtifactVersion: publication.Version, ChartArtifactDigest: publication.ArtifactDigest, TreeID: tree.ID, TreeVersion: tree.Version, TreeDigest: tree.TreeDigest}
	_, err = privatefile.ReadFile(journal, 16384)
	if errors.Is(err, os.ErrNotExist) {
		data, e := json.MarshalIndent(saved, "", "  ")
		if e != nil {
			return "", errors.New("workload recovery metadata unavailable")
		}
		if e = writeNVKEExclusive(journal, append(data, '\n')); e != nil {
			return "", e
		}
	} else if err != nil {
		return "", errors.New("preserve the original private workload recovery journal")
	}
	data, err := privatefile.ReadFile(journal, 16384)
	var retained nvkeWorkloadJournal
	if err != nil || decodeNVKEWorkloadStrict(data, &retained) != nil || !reflect.DeepEqual(saved, retained) {
		return "", errors.New("workload recovery source or exact private input changed; preserve the original artifact and journal")
	}
	return string(text), nil
}

func rejectNVKEWorkloadDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var value func(int) error
	value = func(depth int) error {
		if depth > 16 {
			return errors.New("workload request nesting exceeds its bound")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				token, err = decoder.Token()
				key, ok := token.(string)
				if err != nil || !ok || seen[key] {
					return errors.New("duplicate workload request field")
				}
				seen[key] = true
				if err = value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err = value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errors.New("invalid workload request")
		}
		_, err = decoder.Token()
		return err
	}
	if !json.Valid(data) {
		return errors.New("invalid workload request")
	}
	return value(0)
}

func validateNVKEInstallPlanWorkload(row *api.NVKEInstallPlanWorkload, plan *api.NVKEInstallPlan, publication *api.NVKEInstallPlanCharts, tree *api.NVKEInstallPlanTree, artifactID string) error {
	invalid := errors.New("workload evidence did not confirm the exact reviewed source; preserve its identity and refresh")
	if row == nil || row.ID != artifactID || row.Version < 1 || row.Status != "prepared" || row.PlanID != plan.ID || row.PlanVersion != plan.Version || row.PlanDigest != plan.Plan.PlanDigest || row.ChartArtifactID != publication.ID || row.ChartArtifactVersion != publication.Version || row.ChartArtifactDigest != publication.ArtifactDigest || row.TreeID != tree.ID || row.TreeVersion != tree.Version || row.TreeDigest != tree.TreeDigest || row.OperationID != plan.OperationID || row.OperationVersion != plan.OperationVersion || row.SiteID != plan.SiteID || row.SiteVersion != plan.SiteVersion || row.AuthorityID != plan.AuthorityID || row.AuthorityVersion != plan.AuthorityVersion || row.ItemCount == nil || *row.ItemCount != plan.ItemCount || row.ResourceCount == nil || *row.ResourceCount < 0 || *row.ResourceCount > 10000 || row.ImageUseCount == nil || *row.ImageUseCount < 0 || row.ImageReferenceCount == nil || *row.ImageReferenceCount < 0 || *row.ImageReferenceCount > *row.ImageUseCount || row.IssueCount == nil || *row.IssueCount < 0 || row.ContinuationAuthorized == nil || *row.ContinuationAuthorized || row.ExecutionState != "requires-reviewed-operator-images-platform-credentials-rewrite-import-use-and-linked-admission" {
		return invalid
	}
	for _, digest := range []string{row.ArtifactDigest, row.SourceDigest, row.CaptureDigest, row.WorkloadDigest} {
		if !nvkeDigestPattern.MatchString(digest) {
			return invalid
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, row.ObservedAt); err != nil {
		return invalid
	}
	return nil
}

func runNVKEAgentInstallPlanWorkload(cmd *cobra.Command, ctx context.Context, c *api.Client, id, verb string) error {
	planID, artifactID, digest := resourceStringFlag(cmd, "plan"), resourceStringFlag(cmd, "artifact"), resourceStringFlag(cmd, "plan-digest")
	treeID, treeDigest := resourceStringFlag(cmd, "tree"), resourceStringFlag(cmd, "tree-digest")
	treeVersion, _ := cmd.Flags().GetInt("tree-version")
	chartID, chartDigest := resourceStringFlag(cmd, "chart-artifact"), resourceStringFlag(cmd, "chart-artifact-digest")
	version, _ := cmd.Flags().GetInt("plan-version")
	chartVersion, _ := cmd.Flags().GetInt("chart-artifact-version")
	identities := map[string]bool{}
	for _, value := range []string{id, planID, chartID, treeID, artifactID} {
		if nvkeGUID(value) != nil || identities[value] {
			return errors.New("review distinct exact original, plan, chart, tree and workload UUIDs")
		}
		identities[value] = true
	}
	if version < 1 || chartVersion < 1 || treeVersion < 1 || !nvkeDigestPattern.MatchString(treeDigest) || !nvkeDigestPattern.MatchString(digest) || !nvkeDigestPattern.MatchString(chartDigest) {
		return errors.New("review exact plan and chart publication versions and SHA-256 digests")
	}
	review, source, err := nvkeAgentJobReview(cmd, ctx, c, id)
	if err != nil {
		return err
	}
	if source.Status != "UNCERTAIN" || source.OperationKey != "tooling.management.install" {
		return errors.New("workload capture requires the original uncertain management installation")
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
		return errors.New("the prepared plan changed; review before workload preparation")
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
		return errors.New("the prepared chart publication changed; review before workload capture")
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
		return errors.New("the execution tree changed; review before workload capture")
	}

	workloadSource := api.NVKEInstallPlanWorkloadSource{NVKEInstallPlanTreeSource: input, TreeID: treeID, ExpectedTreeVersion: treeVersion, TreeDigest: treeDigest}
	var row *api.NVKEInstallPlanWorkload
	var maximumResources int
	if verb == "prepare" {
		text, e := nvkeInstallPlanWorkloadIntent(cmd, c, review, source, plan, publication, tree, artifactID)
		if e != nil {
			return e
		}
		var limits struct {
			MaximumResources int `json:"maximumResources"`
		}
		if json.Unmarshal([]byte(text), &limits) != nil {
			return errors.New("workload limits unavailable")
		}
		maximumResources = limits.MaximumResources
		row, err = c.PrepareNVKEInstallPlanWorkload(ctx, api.NVKEInstallPlanWorkloadPrepare{NVKEInstallPlanWorkloadSource: workloadSource, RequestText: text})
	} else {
		row, err = c.ReviewNVKEInstallPlanWorkload(ctx, api.NVKEInstallPlanWorkloadReview{NVKEInstallPlanWorkloadSource: workloadSource, ArtifactID: artifactID}, verb == "retry-preparation")
	}
	if err != nil {
		return fmt.Errorf("%w; retain --artifact %s and exact source/publication versions; use install-plan workload review before another preparation", err, artifactID)
	}
	if err = validateNVKEInstallPlanWorkload(row, plan, publication, tree, artifactID); err != nil {
		return err
	}
	if verb == "prepare" && *row.ResourceCount > maximumResources {
		return errors.New("workload response exceeds its reviewed resource bound")
	}
	return nvkeJSON(cmd, row)
}
