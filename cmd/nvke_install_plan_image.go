package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/spf13/cobra"
)

func newNVKEAgentInstallPlanImageCommand() *cobra.Command {
	group := &cobra.Command{Use: "image", Short: "Prepare and recover a source-bound private registry or local image artifact"}
	for _, verb := range []string{"prepare", "review", "retry-preparation"} {
		action := verb
		leaf := &cobra.Command{Use: verb + " OPERATION_GUID", Args: cobra.ExactArgs(1),
			Short: verb + " an image artifact for the exact plan and chart publication",
			Long: `Image preparation seals a private recovery journal before the API request.
Review reads retained image evidence. Explicit retry uses the server's retained
private intent and its original caller/token. Workload, platform, credential and
fresh use-time evidence remain required for native admission.`,
			RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
				return runNVKEAgentInstallPlanImage(cmd, ctx, c, args[0], action)
			})}
		nvkeAgentJobFlags(leaf)
		leaf.Flags().String("workload", "", "Exact reviewed workload inventory UUID (optional registry source)")
		leaf.Flags().Int("workload-version", 0, "Exact reviewed workload inventory version")
		leaf.Flags().String("workload-digest", "", "Exact reviewed workload inventory SHA-256")
		leaf.Flags().String("source-kind", "registry", "Image source: registry or local; must match retained intent")
		leaf.Flags().String("plan", "", "Exact prepared plan UUID (required)")
		leaf.Flags().Int("plan-version", 0, "Exact prepared plan version (required)")
		leaf.Flags().String("plan-digest", "", "Exact prepared plan SHA-256 (required)")
		leaf.Flags().String("chart-artifact", "", "Exact chart publication UUID (required)")
		leaf.Flags().Int("chart-artifact-version", 0, "Exact chart publication version (required)")
		leaf.Flags().String("chart-artifact-digest", "", "Exact chart publication SHA-256 (required)")
		leaf.Flags().String("artifact", "", "Exact new or retained image UUID (required)")
		leaf.Flags().String("tree", "", "Exact execution-tree UUID (required)")
		leaf.Flags().Int("tree-version", 0, "Exact reviewed execution-tree version (required)")
		leaf.Flags().String("tree-digest", "", "Exact execution-tree SHA-256 (required)")
		for _, flag := range []string{"plan", "plan-version", "plan-digest", "chart-artifact", "chart-artifact-version", "chart-artifact-digest", "artifact", "tree", "tree-version", "tree-digest"} {
			_ = leaf.MarkFlagRequired(flag)
		}
		if verb == "prepare" {
			leaf.Flags().String("file", "", "Private complete image JSON request or - for stdin (required)")
			leaf.Flags().String("request-file", "", "New or matching private metadata recovery journal (required)")
			_ = leaf.MarkFlagRequired("file")
			_ = leaf.MarkFlagRequired("request-file")
		}
		group.AddCommand(leaf)
	}
	return group
}

type nvkeImageJournal struct {
	WorkloadID      string `json:"workloadId,omitempty"`
	WorkloadVersion int    `json:"workloadVersion,omitempty"`
	WorkloadDigest  string `json:"workloadDigest,omitempty"`
	SourceKind      string `json:"sourceKind,omitempty"`
	nvkeChartJournal
	ChartArtifactID      string `json:"chartArtifactId"`
	ChartArtifactVersion int    `json:"chartArtifactVersion"`
	ChartArtifactDigest  string `json:"chartArtifactDigest"`
	TreeID               string `json:"treeId"`
	TreeVersion          int    `json:"treeVersion"`
	TreeDigest           string `json:"treeDigest"`
}

func decodeNVKEImageStrict(data []byte, target any) error {
	allowed := map[string]bool{
		"workloadId": true, "workloadVersion": true, "workloadDigest": true,
		"format": true, "artifactId": true, "planId": true, "planDigest": true, "maximumBytes": true,
		"sourceKind": true, "reference": true, "policy": true, "maximumCalls": true, "timeoutSeconds": true,
		"chartArtifactId": true, "chartArtifactVersion": true, "chartArtifactDigest": true,
		"kind": true, "server": true, "organizationId": true, "actorId": true, "siteId": true,
		"siteVersion": true, "authorityVersion": true, "operationId": true, "operationVersion": true,
		"inputSha256": true, "planVersion": true, "treeId": true, "treeVersion": true, "treeDigest": true,
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return errors.New("image metadata must be one JSON object")
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, e := decoder.Token()
		key, ok := token.(string)
		if e != nil || !ok || !allowed[key] || seen[key] {
			return errors.New("duplicate or invalid image metadata fields")
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return errors.New("invalid image metadata value")
		}
	}
	if !json.Valid(data) {
		return errors.New("image metadata must be one JSON object")
	}
	return decodeNVKEStrict(data, target)
}

func nvkeInstallPlanImageIntent(cmd *cobra.Command, c *api.Client, review api.NVKEAgentJobReview, source *api.NVKEOperation, plan *api.NVKEInstallPlan, publication *api.NVKEInstallPlanCharts, tree *api.NVKEInstallPlanTree, artifactID string) (string, error) {
	file, journal := resourceStringFlag(cmd, "file"), resourceStringFlag(cmd, "request-file")
	if file == "" || journal == "" || journal == "-" {
		return "", errors.New("use a private image input and a private recovery journal path")
	}
	text, err := readNVKEText(cmd, file)
	if err != nil {
		return "", err
	}
	var body struct {
		WorkloadID     string `json:"workloadId"`
		WorkloadDigest string `json:"workloadDigest"`
		SourceKind     string `json:"sourceKind"`
		Format         int    `json:"format"`
		ArtifactID     string `json:"artifactId"`
		PlanID         string `json:"planId"`
		PlanDigest     string `json:"planDigest"`
		TreeID         string `json:"treeId"`
		TreeDigest     string `json:"treeDigest"`
		Reference      string `json:"reference"`
		Policy         string `json:"policy"`
		MaximumBytes   string `json:"maximumBytes"`
		MaximumCalls   int    `json:"maximumCalls"`
		TimeoutSeconds int    `json:"timeoutSeconds"`
	}
	if len(text) > 1024*1024 || decodeNVKEImageStrict(text, &body) != nil || body.Format != 1 || body.ArtifactID != artifactID || body.PlanID != plan.ID || body.PlanDigest != plan.Plan.PlanDigest || body.TreeID != tree.ID || body.TreeDigest != tree.TreeDigest || !validNVKEImageReference(body.Reference) {
		return "", errors.New("review a closed image request matching the exact artifact, plan and tree")
	}
	kind := resourceStringFlag(cmd, "source-kind")
	var fields map[string]json.RawMessage
	if json.Unmarshal(text, &fields) != nil {
		return "", errors.New("invalid closed image request")
	}
	local := kind == "local"
	expected := []string{"format", "artifactId", "planId", "planDigest", "treeId", "treeDigest", "reference", "maximumBytes", "timeoutSeconds"}
	if local {
		expected = append(expected, "sourceKind")
		parts := strings.Split(body.Reference, "@")
		if body.SourceKind != "local" || len(parts) != 2 || !nvkeImageContentDigest.MatchString(parts[1]) {
			return "", errors.New("local image requires a digest-pinned reference and local source kind")
		}
	} else {
		expected = append(expected, "policy", "maximumCalls")
		workloadID, workloadDigest := resourceStringFlag(cmd, "workload"), resourceStringFlag(cmd, "workload-digest")
		if workloadID != "" {
			expected = append(expected, "workloadId", "workloadDigest")
			if body.WorkloadID != workloadID || body.WorkloadDigest != workloadDigest {
				return "", errors.New("image request must match the exact reviewed workload inventory")
			}
		}
		if kind != "registry" || !nvkePlanItemName.MatchString(body.Policy) || body.MaximumCalls < 1 || body.MaximumCalls > 4096 {
			return "", errors.New("registry image requires a reviewed policy and bounded call count")
		}
	}
	if len(fields) != len(expected) {
		return "", errors.New("image request fields do not match the selected source kind")
	}
	for _, key := range expected {
		if _, ok := fields[key]; !ok || bytes.Equal(bytes.TrimSpace(fields[key]), []byte("null")) {
			return "", errors.New("missing image request field")
		}
	}
	limit, err := agentDecimal(body.MaximumBytes)
	if err != nil || limit < 1 || body.TimeoutSeconds < 1 || body.TimeoutSeconds > 300 {
		return "", errors.New("use canonical bounded image bytes, calls and timeout")
	}

	saved := nvkeImageJournal{nvkeChartJournal: nvkeChartJournal{nvkePlanJournal: nvkePlanJournal{
		Format: 1, Kind: "nvke-install-plan-image", Server: c.BaseURL(), OrganizationID: c.Org(),
		ActorID: review.ExpectedActorID, SiteID: source.SiteID, SiteVersion: review.ExpectedSiteVersion,
		AuthorityVersion: review.ExpectedAuthorityVersion, OperationID: source.ID,
		OperationVersion: review.ExpectedOperationVersion, PlanID: plan.ID, InputSHA256: nvkeParameterDigest(text),
	}, PlanVersion: plan.Version, PlanDigest: plan.Plan.PlanDigest, ArtifactID: artifactID},
		ChartArtifactID: publication.ID, ChartArtifactVersion: publication.Version, ChartArtifactDigest: publication.ArtifactDigest, TreeID: tree.ID, TreeVersion: tree.Version, TreeDigest: tree.TreeDigest}
	if local {
		saved.SourceKind = "local"
	} else {
		saved.WorkloadID = resourceStringFlag(cmd, "workload")
		saved.WorkloadVersion, _ = cmd.Flags().GetInt("workload-version")
		saved.WorkloadDigest = resourceStringFlag(cmd, "workload-digest")
	}
	_, err = privatefile.ReadFile(journal, 16384)
	if errors.Is(err, os.ErrNotExist) {
		data, e := json.MarshalIndent(saved, "", "  ")
		if e != nil {
			return "", errors.New("image recovery metadata unavailable")
		}
		if e = writeNVKEExclusive(journal, append(data, '\n')); e != nil {
			return "", e
		}
	} else if err != nil {
		return "", errors.New("preserve the original private image recovery journal")
	}
	data, err := privatefile.ReadFile(journal, 16384)
	var retained nvkeImageJournal
	if err != nil || decodeNVKEImageStrict(data, &retained) != nil || !reflect.DeepEqual(saved, retained) {
		return "", errors.New("image recovery source or exact private input changed; preserve the original artifact and journal")
	}
	return string(text), nil
}

func validNVKEImageReference(value string) bool {
	if len(value) == 0 || len(value) > 4096 || strings.Contains(value, "://") || strings.ContainsAny(value, "?#\\") {
		return false
	}
	for _, char := range value {
		if char <= 32 || char >= 127 {
			return false
		}
	}
	return true
}

var nvkeImageContentDigest = regexp.MustCompile(`^(sha256:[0-9a-f]{64}|sha512:[0-9a-f]{128})$`)

func validateNVKEInstallPlanImage(row *api.NVKEInstallPlanImage, plan *api.NVKEInstallPlan, publication *api.NVKEInstallPlanCharts, tree *api.NVKEInstallPlanTree, artifactID string, kinds ...string) error {
	kind := "registry"
	if len(kinds) > 0 {
		kind = kinds[0]
	}
	invalid := errors.New("image evidence did not confirm the exact reviewed source; preserve its identity and refresh")
	if row == nil || (kind != "registry" && kind != "local") || (row.SourceKind != kind && (kind != "registry" || row.SourceKind != "")) || row.ID != artifactID || row.Version < 1 || row.Status != "prepared" || row.PlanID != plan.ID || row.PlanVersion != plan.Version || row.PlanDigest != plan.Plan.PlanDigest || row.ChartArtifactID != publication.ID || row.ChartArtifactVersion != publication.Version || row.ChartArtifactDigest != publication.ArtifactDigest || row.TreeID != tree.ID || row.TreeVersion != tree.Version || row.TreeDigest != tree.TreeDigest || row.OperationID != plan.OperationID || row.OperationVersion != plan.OperationVersion || row.SiteID != plan.SiteID || row.SiteVersion != plan.SiteVersion || row.AuthorityID != plan.AuthorityID || row.AuthorityVersion != plan.AuthorityVersion || row.BlobCount == nil || *row.BlobCount < 1 || *row.BlobCount > 100000 || row.ContinuationAuthorized == nil || *row.ContinuationAuthorized || row.ExecutionState != "requires-workload-platform-credential-phase-use-and-linked-admission" {
		return invalid
	}
	for _, digest := range []string{row.ArtifactDigest, row.SourceDigest, row.CaptureDigest} {
		if !nvkeDigestPattern.MatchString(digest) {
			return invalid
		}
	}
	if !nvkeImageContentDigest.MatchString(row.RootDigest) {
		return invalid
	}
	if size, err := agentDecimal(row.Bytes); err != nil || size < 1 {
		return invalid
	}
	if _, err := time.Parse(time.RFC3339Nano, row.ObservedAt); err != nil {
		return invalid
	}
	return nil
}

func runNVKEAgentInstallPlanImage(cmd *cobra.Command, ctx context.Context, c *api.Client, id, verb string) error {
	kind := resourceStringFlag(cmd, "source-kind")
	if kind != "registry" && kind != "local" {
		return errors.New("source-kind must be registry or local")
	}
	planID, artifactID, digest := resourceStringFlag(cmd, "plan"), resourceStringFlag(cmd, "artifact"), resourceStringFlag(cmd, "plan-digest")
	treeID, treeDigest := resourceStringFlag(cmd, "tree"), resourceStringFlag(cmd, "tree-digest")
	treeVersion, _ := cmd.Flags().GetInt("tree-version")
	chartID, chartDigest := resourceStringFlag(cmd, "chart-artifact"), resourceStringFlag(cmd, "chart-artifact-digest")
	version, _ := cmd.Flags().GetInt("plan-version")
	chartVersion, _ := cmd.Flags().GetInt("chart-artifact-version")
	identities := map[string]bool{}
	for _, value := range []string{id, planID, chartID, treeID, artifactID} {
		if nvkeGUID(value) != nil || identities[value] {
			return errors.New("review distinct exact original, plan, chart, tree and image UUIDs")
		}
		identities[value] = true
	}
	if version < 1 || chartVersion < 1 || treeVersion < 1 || !nvkeDigestPattern.MatchString(treeDigest) || !nvkeDigestPattern.MatchString(digest) || !nvkeDigestPattern.MatchString(chartDigest) {
		return errors.New("review exact plan and chart publication versions and SHA-256 digests")
	}
	workloadID, workloadDigest := resourceStringFlag(cmd, "workload"), resourceStringFlag(cmd, "workload-digest")
	workloadVersion, _ := cmd.Flags().GetInt("workload-version")
	linked := workloadID != "" || workloadDigest != "" || cmd.Flags().Changed("workload-version")
	if linked && (kind != "registry" || nvkeGUID(workloadID) != nil || identities[workloadID] || workloadVersion < 1 || !nvkeDigestPattern.MatchString(workloadDigest)) {
		return errors.New("review a distinct workload UUID, exact version and SHA-256 for registry acquisition")
	}
	review, source, err := nvkeAgentJobReview(cmd, ctx, c, id)
	if err != nil {
		return err
	}
	if source.Status != "UNCERTAIN" || source.OperationKey != "tooling.management.install" {
		return errors.New("image capture requires the original uncertain management installation")
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
		return errors.New("the prepared plan changed; review before image preparation")
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
		return errors.New("the prepared chart publication changed; review before image capture")
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
		return errors.New("the execution tree changed; review before image capture")
	}

	imageSource := api.NVKEInstallPlanImageSource{NVKEInstallPlanTreeSource: input, TreeID: treeID, ExpectedTreeVersion: treeVersion, TreeDigest: treeDigest}
	if linked {
		workload, e := c.ReviewNVKEInstallPlanWorkload(ctx, api.NVKEInstallPlanWorkloadReview{
			NVKEInstallPlanWorkloadSource: api.NVKEInstallPlanWorkloadSource{
				NVKEInstallPlanTreeSource: input, TreeID: treeID, ExpectedTreeVersion: treeVersion, TreeDigest: treeDigest,
			}, ArtifactID: workloadID,
		}, false)
		if e != nil {
			return e
		}
		if e = validateNVKEInstallPlanWorkload(workload, plan, publication, tree, workloadID); e != nil {
			return e
		}
		if workload.Version != workloadVersion || workload.WorkloadDigest != workloadDigest {
			return errors.New("the workload inventory changed; review before image acquisition")
		}
		imageSource.WorkloadID, imageSource.ExpectedWorkloadVersion, imageSource.WorkloadDigest = workloadID, workloadVersion, workloadDigest
	}
	var row *api.NVKEInstallPlanImage
	var maximumBytes int64
	var requestedRoot string
	if verb == "prepare" {
		var text string
		text, err = nvkeInstallPlanImageIntent(cmd, c, review, source, plan, publication, tree, artifactID)
		if err != nil {
			return err
		}
		var limits struct {
			MaximumBytes string `json:"maximumBytes"`
			Reference    string `json:"reference"`
		}
		if json.Unmarshal([]byte(text), &limits) != nil {
			return errors.New("image limits unavailable")
		}
		if kind == "local" {
			requestedRoot = strings.Split(limits.Reference, "@")[1]
		}
		maximumBytes, err = agentDecimal(limits.MaximumBytes)
		if err != nil {
			return errors.New("image limits unavailable")
		}
		row, err = c.PrepareNVKEInstallPlanImage(ctx, api.NVKEInstallPlanImagePrepare{NVKEInstallPlanImageSource: imageSource, RequestText: text})
	} else {
		row, err = c.ReviewNVKEInstallPlanImage(ctx, api.NVKEInstallPlanImageReview{NVKEInstallPlanImageSource: imageSource, ArtifactID: artifactID}, verb == "retry-preparation")
	}
	if err != nil {
		return fmt.Errorf("%w; retain --artifact %s and exact source/publication versions; use install-plan image review before another preparation", err, artifactID)
	}
	if err = validateNVKEInstallPlanImage(row, plan, publication, tree, artifactID, kind); err != nil {
		return err
	}
	if row.WorkloadID != workloadID || row.WorkloadDigest != workloadDigest ||
		(linked && (row.WorkloadVersion == nil || *row.WorkloadVersion != workloadVersion)) ||
		(!linked && row.WorkloadVersion != nil) {
		return errors.New("image evidence did not confirm the exact reviewed workload source")
	}
	if verb == "prepare" {
		size, _ := agentDecimal(row.Bytes)
		if size > maximumBytes || (kind == "local" && row.RootDigest != requestedRoot) {
			return errors.New("image response did not confirm the reviewed byte limit and digest")
		}
	}
	return nvkeJSON(cmd, row)
}
