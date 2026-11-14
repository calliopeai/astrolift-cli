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

func newNVKEAgentInstallPlanImageBindingCommand() *cobra.Command {
	group := &cobra.Command{Use: "image-binding", Short: "Prepare and recover a signed workload/image binding observation"}
	for _, verb := range []string{"prepare", "review", "retry-preparation"} {
		action := verb
		leaf := &cobra.Command{Use: verb + " OPERATION_GUID", Args: cobra.ExactArgs(1),
			Short: verb + " a signed image binding for the exact reviewed workload",
			Long: `Image-binding preparation seals a private recovery journal before the API request.
Review reads retained signed image-binding evidence. Explicit retry uses the server's retained
private intent and its original caller/token. Workload, platform, credential and
fresh use-time evidence remain required for native admission.`,
			RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
				return runNVKEAgentInstallPlanImageBinding(cmd, ctx, c, args[0], action)
			})}
		nvkeAgentJobFlags(leaf)
		leaf.Flags().String("plan", "", "Exact prepared plan UUID (required)")
		leaf.Flags().Int("plan-version", 0, "Exact prepared plan version (required)")
		leaf.Flags().String("plan-digest", "", "Exact prepared plan SHA-256 (required)")
		leaf.Flags().String("chart-artifact", "", "Exact chart publication UUID (required)")
		leaf.Flags().Int("chart-artifact-version", 0, "Exact chart publication version (required)")
		leaf.Flags().String("chart-artifact-digest", "", "Exact chart publication SHA-256 (required)")
		leaf.Flags().String("artifact", "", "Exact new or retained image-binding UUID (required)")
		leaf.Flags().String("tree", "", "Exact execution-tree UUID (required)")
		leaf.Flags().Int("tree-version", 0, "Exact reviewed execution-tree version (required)")
		leaf.Flags().String("tree-digest", "", "Exact execution-tree SHA-256 (required)")
		for _, flag := range []string{"plan", "plan-version", "plan-digest", "chart-artifact", "chart-artifact-version", "chart-artifact-digest", "artifact", "tree", "tree-version", "tree-digest"} {
			_ = leaf.MarkFlagRequired(flag)
		}
		if verb == "prepare" {
			leaf.Flags().String("file", "", "Private complete image-binding JSON request or - for stdin (required)")
			leaf.Flags().String("request-file", "", "New or matching private metadata recovery journal (required)")
			_ = leaf.MarkFlagRequired("file")
			_ = leaf.MarkFlagRequired("request-file")
		}
		leaf.Flags().String("workload", "", "Exact reviewed workload UUID (required)")
		leaf.Flags().Int("workload-version", 0, "Exact reviewed workload version (required)")
		leaf.Flags().String("workload-digest", "", "Exact reviewed workload SHA-256 (required)")
		for _, flag := range []string{"workload", "workload-version", "workload-digest"} {
			_ = leaf.MarkFlagRequired(flag)
		}
		group.AddCommand(leaf)
	}
	return group
}

type nvkeImageBindingJournal struct {
	WorkloadID      string `json:"workloadId"`
	WorkloadVersion int    `json:"workloadVersion"`
	WorkloadDigest  string `json:"workloadDigest"`
	nvkeChartJournal
	ChartArtifactID      string `json:"chartArtifactId"`
	ChartArtifactVersion int    `json:"chartArtifactVersion"`
	ChartArtifactDigest  string `json:"chartArtifactDigest"`
	TreeID               string `json:"treeId"`
	TreeVersion          int    `json:"treeVersion"`
	TreeDigest           string `json:"treeDigest"`
}

func decodeNVKEImageBindingStrict(data []byte, target any) error {
	allowed := map[string]bool{
		"format": true, "artifactId": true, "planId": true, "planDigest": true, "maximumBytes": true,
		"imageBindings": true, "maximumImageBytes": true, "timeoutSeconds": true,
		"workloadId": true, "workloadVersion": true, "workloadDigest": true,
		"chartArtifactId": true, "chartArtifactVersion": true, "chartArtifactDigest": true,
		"kind": true, "server": true, "organizationId": true, "actorId": true, "siteId": true,
		"siteVersion": true, "authorityVersion": true, "operationId": true, "operationVersion": true,
		"inputSha256": true, "planVersion": true, "treeId": true, "treeVersion": true, "treeDigest": true,
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return errors.New("image-binding metadata must be one JSON object")
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, e := decoder.Token()
		key, ok := token.(string)
		if e != nil || !ok || !allowed[key] || seen[key] {
			return errors.New("duplicate or invalid image-binding metadata fields")
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return errors.New("invalid image-binding metadata value")
		}
	}
	if !json.Valid(data) {
		return errors.New("image-binding metadata must be one JSON object")
	}
	return decodeNVKEStrict(data, target)
}

func nvkeInstallPlanImageBindingIntent(cmd *cobra.Command, c *api.Client, review api.NVKEAgentJobReview, source *api.NVKEOperation, plan *api.NVKEInstallPlan, publication *api.NVKEInstallPlanCharts, tree *api.NVKEInstallPlanTree, workload *api.NVKEInstallPlanWorkload, artifactID string) (string, error) {
	file, journal := resourceStringFlag(cmd, "file"), resourceStringFlag(cmd, "request-file")
	if file == "" || journal == "" || journal == "-" {
		return "", errors.New("use a private image-binding input and a private recovery journal path")
	}
	text, err := readNVKEText(cmd, file)
	if err != nil {
		return "", err
	}
	var body struct {
		Format         int    `json:"format"`
		ArtifactID     string `json:"artifactId"`
		PlanID         string `json:"planId"`
		PlanDigest     string `json:"planDigest"`
		TreeID         string `json:"treeId"`
		TreeDigest     string `json:"treeDigest"`
		WorkloadID     string `json:"workloadId"`
		WorkloadDigest string `json:"workloadDigest"`
		ImageBindings  map[string]struct {
			SourceKind     string `json:"sourceKind"`
			ArtifactID     string `json:"artifactId"`
			ArtifactDigest string `json:"artifactDigest"`
		} `json:"imageBindings"`
		MaximumBytes      string `json:"maximumBytes"`
		MaximumImageBytes string `json:"maximumImageBytes"`
		TimeoutSeconds    int    `json:"timeoutSeconds"`
	}
	if len(text) > 1024*1024 || rejectNVKEWorkloadDuplicateKeys(text) != nil || decodeNVKEImageBindingStrict(text, &body) != nil || body.Format != 1 || body.ArtifactID != artifactID || body.PlanID != plan.ID || body.PlanDigest != plan.Plan.PlanDigest || body.TreeID != tree.ID || body.TreeDigest != tree.TreeDigest || body.WorkloadID != workload.ID || body.WorkloadDigest != workload.WorkloadDigest {
		return "", errors.New("review a closed image-binding request matching the exact artifact, plan and tree")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(text, &fields) != nil || len(fields) != 12 {
		return "", errors.New("invalid closed image-binding request")
	}
	for _, key := range []string{"format", "artifactId", "planId", "planDigest", "treeId", "treeDigest", "workloadId", "workloadDigest", "imageBindings", "maximumBytes", "maximumImageBytes", "timeoutSeconds"} {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return "", errors.New("missing image-binding request field")
		}
	}
	var rawBindings map[string]map[string]json.RawMessage
	if json.Unmarshal(fields["imageBindings"], &rawBindings) != nil || len(rawBindings) > 10000 {
		return "", errors.New("invalid image bindings")
	}
	for _, row := range rawBindings {
		if len(row) != 3 {
			return "", errors.New("use closed image proof bindings")
		}
		for _, key := range []string{"sourceKind", "artifactId", "artifactDigest"} {
			if row[key] == nil || bytes.Equal(bytes.TrimSpace(row[key]), []byte("null")) {
				return "", errors.New("use closed image proof bindings")
			}
		}
	}
	ids := map[string]bool{source.ID: true, plan.ID: true, publication.ID: true, tree.ID: true, workload.ID: true, artifactID: true}
	for reference, row := range body.ImageBindings {
		if !validNVKEImageReference(reference) || (row.SourceKind != "local" && row.SourceKind != "registry") || nvkeGUID(row.ArtifactID) != nil || ids[row.ArtifactID] || !nvkeDigestPattern.MatchString(row.ArtifactDigest) {
			return "", errors.New("use reviewed image references and exact retained proof selections")
		}
	}
	limit, err := agentDecimal(body.MaximumBytes)
	imageLimit, imageErr := agentDecimal(body.MaximumImageBytes)
	if err != nil || imageErr != nil || imageLimit < 1 || limit < 1 || limit > 64*1024*1024 || body.TimeoutSeconds < 1 || body.TimeoutSeconds > 300 {
		return "", errors.New("use bounded image, output and timeout budgets")
	}

	saved := nvkeImageBindingJournal{WorkloadID: workload.ID, WorkloadVersion: workload.Version, WorkloadDigest: workload.WorkloadDigest, nvkeChartJournal: nvkeChartJournal{nvkePlanJournal: nvkePlanJournal{
		Format: 1, Kind: "nvke-install-plan-image-binding", Server: c.BaseURL(), OrganizationID: c.Org(),
		ActorID: review.ExpectedActorID, SiteID: source.SiteID, SiteVersion: review.ExpectedSiteVersion,
		AuthorityVersion: review.ExpectedAuthorityVersion, OperationID: source.ID,
		OperationVersion: review.ExpectedOperationVersion, PlanID: plan.ID, InputSHA256: nvkeParameterDigest(text),
	}, PlanVersion: plan.Version, PlanDigest: plan.Plan.PlanDigest, ArtifactID: artifactID},
		ChartArtifactID: publication.ID, ChartArtifactVersion: publication.Version, ChartArtifactDigest: publication.ArtifactDigest, TreeID: tree.ID, TreeVersion: tree.Version, TreeDigest: tree.TreeDigest}
	_, err = privatefile.ReadFile(journal, 16384)
	if errors.Is(err, os.ErrNotExist) {
		data, e := json.MarshalIndent(saved, "", "  ")
		if e != nil {
			return "", errors.New("image-binding recovery metadata unavailable")
		}
		if e = writeNVKEExclusive(journal, append(data, '\n')); e != nil {
			return "", e
		}
	} else if err != nil {
		return "", errors.New("preserve the original private image-binding recovery journal")
	}
	data, err := privatefile.ReadFile(journal, 16384)
	var retained nvkeImageBindingJournal
	if err != nil || decodeNVKEImageBindingStrict(data, &retained) != nil || !reflect.DeepEqual(saved, retained) {
		return "", errors.New("image-binding recovery source or exact private input changed; preserve the original artifact and journal")
	}
	return string(text), nil
}

func validateNVKEInstallPlanImageBinding(row *api.NVKEInstallPlanImageBinding, plan *api.NVKEInstallPlan, publication *api.NVKEInstallPlanCharts, tree *api.NVKEInstallPlanTree, workload *api.NVKEInstallPlanWorkload, artifactID string) error {
	invalid := errors.New("signed image binding did not confirm the exact reviewed source")
	if workload == nil || workload.ImageReferenceCount == nil || workload.ImageUseCount == nil || workload.IssueCount == nil || row == nil || row.ID != artifactID || row.Version < 1 || row.Status != "prepared" || row.PlanID != plan.ID || row.PlanVersion != plan.Version || row.PlanDigest != plan.Plan.PlanDigest || row.ChartArtifactID != publication.ID || row.ChartArtifactVersion != publication.Version || row.ChartArtifactDigest != publication.ArtifactDigest || row.TreeID != tree.ID || row.TreeVersion != tree.Version || row.TreeDigest != tree.TreeDigest || row.WorkloadID != workload.ID || row.WorkloadVersion != workload.Version || row.WorkloadDigest != workload.WorkloadDigest || row.OperationID != plan.OperationID || row.OperationVersion != plan.OperationVersion || row.SiteID != plan.SiteID || row.SiteVersion != plan.SiteVersion || row.AuthorityID != plan.AuthorityID || row.AuthorityVersion != plan.AuthorityVersion || row.ImageCount == nil || *row.ImageCount < *workload.ImageReferenceCount || *row.ImageCount > 10000 || row.ImageUseCount == nil || *row.ImageUseCount != *workload.ImageUseCount || row.WorkloadIssueCount == nil || *row.WorkloadIssueCount != *workload.IssueCount || row.ImportCount == nil || *row.ImportCount < 0 || *row.ImportCount > 10000 || row.RewriteCount == nil || *row.RewriteCount < 0 || *row.RewriteCount > *row.ImageCount || row.ContinuationAuthorized == nil || *row.ContinuationAuthorized || row.ExecutionState != "requires-reviewed-operator-images-node-platform-credentials-rewrite-import-use-and-linked-admission" {
		return invalid
	}
	for _, digest := range []string{row.ArtifactDigest, row.SourceDigest, row.CaptureDigest, row.BindingDigest} {
		if !nvkeDigestPattern.MatchString(digest) {
			return invalid
		}
	}
	size, err := agentDecimal(row.VerifiedImageBytes)
	if err != nil || (*row.ImageCount == 0) != (size == 0) {
		return invalid
	}
	observed, err := time.Parse(time.RFC3339Nano, row.ObservedAt)
	original, sourceErr := time.Parse(time.RFC3339Nano, workload.ObservedAt)
	if err != nil || sourceErr != nil || observed.Before(original) {
		return invalid
	}
	return nil
}

func runNVKEAgentInstallPlanImageBinding(cmd *cobra.Command, ctx context.Context, c *api.Client, id, verb string) error {
	planID, artifactID, digest := resourceStringFlag(cmd, "plan"), resourceStringFlag(cmd, "artifact"), resourceStringFlag(cmd, "plan-digest")
	treeID, treeDigest := resourceStringFlag(cmd, "tree"), resourceStringFlag(cmd, "tree-digest")
	treeVersion, _ := cmd.Flags().GetInt("tree-version")
	chartID, chartDigest := resourceStringFlag(cmd, "chart-artifact"), resourceStringFlag(cmd, "chart-artifact-digest")
	version, _ := cmd.Flags().GetInt("plan-version")
	chartVersion, _ := cmd.Flags().GetInt("chart-artifact-version")
	workloadID, workloadDigest := resourceStringFlag(cmd, "workload"), resourceStringFlag(cmd, "workload-digest")
	workloadVersion, _ := cmd.Flags().GetInt("workload-version")
	identities := map[string]bool{}
	for _, value := range []string{id, planID, chartID, treeID, artifactID, workloadID} {
		if nvkeGUID(value) != nil || identities[value] {
			return errors.New("review distinct exact original, plan, chart, tree and workload UUIDs")
		}
		identities[value] = true
	}
	if workloadVersion < 1 || !nvkeDigestPattern.MatchString(workloadDigest) || version < 1 || chartVersion < 1 || treeVersion < 1 || !nvkeDigestPattern.MatchString(treeDigest) || !nvkeDigestPattern.MatchString(digest) || !nvkeDigestPattern.MatchString(chartDigest) {
		return errors.New("review exact plan and chart publication versions and SHA-256 digests")
	}
	review, source, err := nvkeAgentJobReview(cmd, ctx, c, id)
	if err != nil {
		return err
	}
	if source.Status != "UNCERTAIN" || source.OperationKey != "tooling.management.install" {
		return errors.New("image binding requires the original uncertain management installation")
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
		return errors.New("the prepared plan changed; review before image binding")
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
		return errors.New("the prepared chart publication changed; review before image binding")
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
		return errors.New("the execution tree changed; review before image binding")
	}

	workloadSource := api.NVKEInstallPlanWorkloadSource{NVKEInstallPlanTreeSource: input, TreeID: treeID, ExpectedTreeVersion: treeVersion, TreeDigest: treeDigest}
	workload, err := c.ReviewNVKEInstallPlanWorkload(ctx, api.NVKEInstallPlanWorkloadReview{NVKEInstallPlanWorkloadSource: workloadSource, ArtifactID: workloadID}, false)
	if err != nil {
		return err
	}
	if err = validateNVKEInstallPlanWorkload(workload, plan, publication, tree, workloadID); err != nil {
		return err
	}
	if workload.Version != workloadVersion || workload.WorkloadDigest != workloadDigest {
		return errors.New("the workload inventory changed; review before binding images")
	}
	bindingSource := api.NVKEInstallPlanImageBindingSource{NVKEInstallPlanWorkloadSource: workloadSource, WorkloadID: workloadID, ExpectedWorkloadVersion: workloadVersion, WorkloadDigest: workloadDigest}
	var row *api.NVKEInstallPlanImageBinding
	var selectedCount int
	var maximumImageBytes int64
	if verb == "prepare" {
		text, e := nvkeInstallPlanImageBindingIntent(cmd, c, review, source, plan, publication, tree, workload, artifactID)
		if e != nil {
			return e
		}
		var limits struct {
			ImageBindings     map[string]json.RawMessage `json:"imageBindings"`
			MaximumImageBytes string                     `json:"maximumImageBytes"`
		}
		if json.Unmarshal([]byte(text), &limits) != nil {
			return errors.New("image-binding limits unavailable")
		}
		selectedCount = len(limits.ImageBindings)
		maximumImageBytes, _ = agentDecimal(limits.MaximumImageBytes)
		row, err = c.PrepareNVKEInstallPlanImageBinding(ctx, api.NVKEInstallPlanImageBindingPrepare{NVKEInstallPlanImageBindingSource: bindingSource, RequestText: text})
	} else {
		row, err = c.ReviewNVKEInstallPlanImageBinding(ctx, api.NVKEInstallPlanImageBindingReview{NVKEInstallPlanImageBindingSource: bindingSource, ArtifactID: artifactID}, verb == "retry-preparation")
	}
	if err != nil {
		return fmt.Errorf("%w; retain --artifact %s and use install-plan image-binding review before another preparation", err, artifactID)
	}
	if err = validateNVKEInstallPlanImageBinding(row, plan, publication, tree, workload, artifactID); err != nil {
		return err
	}
	size, _ := agentDecimal(row.VerifiedImageBytes)
	if verb == "prepare" && (*row.ImageCount != selectedCount || size > maximumImageBytes) {
		return errors.New("image-binding response exceeds its reviewed selection or byte bound")
	}
	return nvkeJSON(cmd, row)
}
