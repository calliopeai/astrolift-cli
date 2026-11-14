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

const nvkeImmutablePayloadState = "requires-reviewed-operator-images-platform-credentials-import-use-and-linked-admission"
const nvkeImmutableTreeState = "requires-fresh-phase-platform-credential-import-use-and-linked-admission"

func nvkeImmutableName(tree bool) string {
	if tree {
		return "immutable-execution-tree"
	}
	return "image-payload"
}

func newNVKEAgentInstallPlanImmutableCommand(tree bool) *cobra.Command {
	name := nvkeImmutableName(tree)
	group := &cobra.Command{Use: name, Short: "Prepare and recover immutable install artifacts"}
	for _, verb := range []string{"prepare", "review", "retry-preparation"} {
		action := verb
		leaf := &cobra.Command{Use: verb + " OPERATION_GUID", Args: cobra.ExactArgs(1), Short: nvkeShortRetry(verb, verb+" an immutable install artifact"), Long: `Preparation saves a private metadata journal before sending the bounded request.
Review reads the retained artifact after a lost response. Explicit retry uses the
server's original private intent and original caller/token. These commands do not
execute the installation or release the original uncertainty fence.`, RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
			return runNVKEAgentInstallPlanImmutable(cmd, ctx, c, args[0], action, tree)
		})}
		nvkeAgentJobFlags(leaf)
		for _, flag := range []string{"plan", "plan-digest", "chart-artifact", "chart-artifact-digest", "tree", "tree-digest", "workload", "workload-digest", "binding", "binding-digest", "artifact"} {
			leaf.Flags().String(flag, "", "Exact reviewed "+flag+" (required)")
			_ = leaf.MarkFlagRequired(flag)
		}
		for _, flag := range []string{"plan-version", "chart-artifact-version", "tree-version", "workload-version", "binding-version"} {
			leaf.Flags().Int(flag, 0, "Exact reviewed "+flag+" (required)")
			_ = leaf.MarkFlagRequired(flag)
		}
		if verb == "prepare" {
			leaf.Flags().String("file", "", "Private complete immutable-artifact JSON request or - for stdin (required)")
			leaf.Flags().String("request-file", "", "New or matching private metadata recovery journal (required)")
			_ = leaf.MarkFlagRequired("file")
			_ = leaf.MarkFlagRequired("request-file")
		}
		group.AddCommand(leaf)
	}
	return group
}

type nvkeImmutableJournal struct {
	nvkeImageBindingJournal
	BindingID      string `json:"bindingId"`
	BindingVersion int    `json:"bindingVersion"`
	BindingDigest  string `json:"bindingDigest"`
}

type nvkeImmutableChoice struct {
	ArtifactID     string `json:"artifactId"`
	ArtifactDigest string `json:"artifactDigest"`
}

type nvkeImmutableBody struct {
	Format           int                            `json:"format"`
	ArtifactID       string                         `json:"artifactId"`
	PlanID           string                         `json:"planId"`
	PlanDigest       string                         `json:"planDigest"`
	TreeID           string                         `json:"treeId"`
	TreeDigest       string                         `json:"treeDigest"`
	BindingID        string                         `json:"bindingId"`
	BindingDigest    string                         `json:"bindingDigest"`
	Item             string                         `json:"item"`
	PayloadBindings  map[string]nvkeImmutableChoice `json:"payloadBindings"`
	MaximumBytes     string                         `json:"maximumBytes"`
	MaximumResources *int                           `json:"maximumResources"`
	TimeoutSeconds   int                            `json:"timeoutSeconds"`
}

func decodeNVKEImmutableStrict(data []byte, target any) error {
	if rejectNVKEWorkloadDuplicateKeys(data) != nil {
		return errors.New("duplicate immutable-artifact metadata")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return errors.New("immutable-artifact metadata must be one object")
	}
	allowed := map[string]bool{}
	for _, name := range []string{"format", "artifactId", "planId", "planDigest", "treeId", "treeDigest", "bindingId", "bindingDigest", "item", "payloadBindings", "maximumBytes", "maximumResources", "timeoutSeconds", "workloadId", "workloadVersion", "workloadDigest", "chartArtifactId", "chartArtifactVersion", "chartArtifactDigest", "kind", "server", "organizationId", "actorId", "siteId", "siteVersion", "authorityVersion", "operationId", "operationVersion", "inputSha256", "planVersion", "treeVersion", "bindingVersion"} {
		allowed[name] = true
	}
	for name := range fields {
		if !allowed[name] {
			return errors.New("unknown or aliased immutable-artifact metadata")
		}
	}
	return decodeNVKEStrict(data, target)
}

func nvkeInstallPlanImmutableIntent(cmd *cobra.Command, c *api.Client, review api.NVKEAgentJobReview, source *api.NVKEOperation, plan *api.NVKEInstallPlan, publication *api.NVKEInstallPlanCharts, executionTree *api.NVKEInstallPlanTree, workload *api.NVKEInstallPlanWorkload, binding *api.NVKEInstallPlanImageBinding, artifactID string, tree bool) (string, nvkeImmutableBody, error) {
	var body nvkeImmutableBody
	invalid := errors.New("use a closed bounded immutable-artifact request for the exact reviewed binding and phase selections")
	file, journal := resourceStringFlag(cmd, "file"), resourceStringFlag(cmd, "request-file")
	if file == "" || journal == "" || journal == "-" {
		return "", body, errors.New("use a private input and a private recovery journal path")
	}
	text, err := readNVKEText(cmd, file)
	if err != nil {
		return "", body, err
	}
	if len(text) > 1024*1024 || decodeNVKEImmutableStrict(text, &body) != nil || body.Format != 1 || body.ArtifactID != artifactID || body.PlanID != plan.ID || body.PlanDigest != plan.Plan.PlanDigest || body.TreeID != executionTree.ID || body.TreeDigest != executionTree.TreeDigest || body.BindingID != binding.ID || body.BindingDigest != binding.BindingDigest {
		return "", body, invalid
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(text, &fields)
	required := []string{"format", "artifactId", "planId", "planDigest", "treeId", "treeDigest", "bindingId", "bindingDigest", "maximumBytes", "timeoutSeconds"}
	if tree {
		required = append(required, "payloadBindings")
	} else {
		required = append(required, "item", "maximumResources")
	}
	if len(fields) != len(required) {
		return "", body, invalid
	}
	for _, name := range required {
		if fields[name] == nil || bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return "", body, invalid
		}
	}
	limit, e := agentDecimal(body.MaximumBytes)
	if e != nil || limit < 1 || (!tree && limit > 64*1024*1024) || body.TimeoutSeconds < 1 || body.TimeoutSeconds > 300 {
		return "", body, invalid
	}
	if tree {
		var rows map[string]map[string]json.RawMessage
		if json.Unmarshal(fields["payloadBindings"], &rows) != nil || len(rows) != plan.ItemCount || len(rows) < 1 || len(rows) > 10000 {
			return "", body, invalid
		}
		reserved := map[string]bool{}
		for _, id := range []string{source.ID, plan.ID, publication.ID, executionTree.ID, workload.ID, binding.ID, artifactID} {
			reserved[id] = true
		}
		for name, row := range rows {
			choice := body.PayloadBindings[name]
			if !nvkePlanItemName.MatchString(name) || len(row) != 2 || row["artifactId"] == nil || row["artifactDigest"] == nil || nvkeGUID(choice.ArtifactID) != nil || reserved[choice.ArtifactID] || !nvkeDigestPattern.MatchString(choice.ArtifactDigest) {
				return "", body, invalid
			}
			reserved[choice.ArtifactID] = true
		}
	} else if !nvkePlanItemName.MatchString(body.Item) || body.MaximumResources == nil || *body.MaximumResources < 1 || *body.MaximumResources > 10000 {
		return "", body, invalid
	}
	saved := nvkeImmutableJournal{nvkeImageBindingJournal: nvkeImageBindingJournal{WorkloadID: workload.ID, WorkloadVersion: workload.Version, WorkloadDigest: workload.WorkloadDigest, nvkeChartJournal: nvkeChartJournal{nvkePlanJournal: nvkePlanJournal{Format: 1, Kind: "nvke-install-plan-" + nvkeImmutableName(tree), Server: c.BaseURL(), OrganizationID: c.Org(), ActorID: review.ExpectedActorID, SiteID: source.SiteID, SiteVersion: review.ExpectedSiteVersion, AuthorityVersion: review.ExpectedAuthorityVersion, OperationID: source.ID, OperationVersion: review.ExpectedOperationVersion, PlanID: plan.ID, InputSHA256: nvkeParameterDigest(text)}, PlanVersion: plan.Version, PlanDigest: plan.Plan.PlanDigest, ArtifactID: artifactID}, ChartArtifactID: publication.ID, ChartArtifactVersion: publication.Version, ChartArtifactDigest: publication.ArtifactDigest, TreeID: executionTree.ID, TreeVersion: executionTree.Version, TreeDigest: executionTree.TreeDigest}, BindingID: binding.ID, BindingVersion: binding.Version, BindingDigest: binding.BindingDigest}
	_, err = privatefile.ReadFile(journal, 16384)
	if errors.Is(err, os.ErrNotExist) {
		data, e := json.MarshalIndent(saved, "", "  ")
		if e != nil {
			return "", body, errors.New("immutable-artifact recovery metadata unavailable")
		}
		if e = writeNVKEExclusive(journal, append(data, '\n')); e != nil {
			return "", body, e
		}
	} else if err != nil {
		return "", body, errors.New("preserve the original private immutable-artifact journal")
	}
	data, err := privatefile.ReadFile(journal, 16384)
	var retained nvkeImmutableJournal
	if err != nil || decodeNVKEImmutableStrict(data, &retained) != nil || !reflect.DeepEqual(saved, retained) {
		return "", body, errors.New("immutable-artifact source or exact private input changed; preserve the original artifact and journal")
	}
	return string(text), body, nil
}

func validateNVKEInstallPlanImmutable(row *api.NVKEInstallPlanImmutableArtifact, plan *api.NVKEInstallPlan, publication *api.NVKEInstallPlanCharts, executionTree *api.NVKEInstallPlanTree, workload *api.NVKEInstallPlanWorkload, binding *api.NVKEInstallPlanImageBinding, artifactID string, tree bool) error {
	invalid := errors.New("immutable artifact did not confirm its exact reviewed source and bounded summary")
	if row == nil || row.ID != artifactID || row.Version < 1 || row.Status != "prepared" || row.PlanID != plan.ID || row.PlanVersion != plan.Version || row.PlanDigest != plan.Plan.PlanDigest || row.ChartArtifactID != publication.ID || row.ChartArtifactVersion != publication.Version || row.ChartArtifactDigest != publication.ArtifactDigest || row.TreeID != executionTree.ID || row.TreeVersion != executionTree.Version || row.TreeDigest != executionTree.TreeDigest || row.WorkloadID != workload.ID || row.WorkloadVersion != workload.Version || row.WorkloadDigest != workload.WorkloadDigest || row.BindingID != binding.ID || row.BindingVersion != binding.Version || row.BindingDigest != binding.BindingDigest || row.OperationID != plan.OperationID || row.OperationVersion != plan.OperationVersion || row.SiteID != plan.SiteID || row.SiteVersion != plan.SiteVersion || row.AuthorityID != plan.AuthorityID || row.AuthorityVersion != plan.AuthorityVersion || row.ContinuationAuthorized == nil || *row.ContinuationAuthorized || row.FileCount == nil || *row.FileCount < 1 {
		return invalid
	}
	for _, digest := range []string{row.ArtifactDigest, row.SourceDigest, row.CaptureDigest} {
		if !nvkeDigestPattern.MatchString(digest) {
			return invalid
		}
	}
	observed, e := time.Parse(time.RFC3339Nano, row.ObservedAt)
	source, se := time.Parse(time.RFC3339Nano, row.SourceObservedAt)
	parent, pe := time.Parse(time.RFC3339Nano, binding.ObservedAt)
	if e != nil || se != nil || pe != nil || !source.Equal(parent) || observed.Before(source) {
		return invalid
	}
	if tree {
		if row.PayloadDigest != "" || row.PayloadBytes != "" || row.ResourceCount != nil || row.ImageUseCount != nil || row.RewriteCount != nil {
			return invalid
		}
		size, e := agentDecimal(row.Bytes)
		if e != nil || size < 1 || !nvkeDigestPattern.MatchString(row.ExecutionDigest) || row.ExecutionState != nvkeImmutableTreeState || *row.FileCount > 100000 || plan.Plan.FirstRemainingOrdinal == nil || row.PhaseCount == nil || *row.PhaseCount < 1 || *row.PhaseCount != plan.ItemCount-*plan.Plan.FirstRemainingOrdinal || row.SkippedPhaseCount == nil || *row.SkippedPhaseCount != *plan.Plan.FirstRemainingOrdinal || row.ImportCount == nil || *row.ImportCount < 0 || binding.ImageCount == nil || *row.ImportCount > *binding.ImageCount {
			return invalid
		}
	} else {
		if row.ExecutionDigest != "" || row.Bytes != "" || row.PhaseCount != nil || row.SkippedPhaseCount != nil || row.ImportCount != nil {
			return invalid
		}
		size, e := agentDecimal(row.PayloadBytes)
		if e != nil || !nvkeDigestPattern.MatchString(row.PayloadDigest) || row.ExecutionState != nvkeImmutablePayloadState || *row.FileCount > 10000 || row.ResourceCount == nil || *row.ResourceCount < 0 || workload.ResourceCount == nil || *row.ResourceCount > *workload.ResourceCount || row.ImageUseCount == nil || *row.ImageUseCount < 0 || binding.ImageUseCount == nil || *row.ImageUseCount > *binding.ImageUseCount || row.RewriteCount == nil || *row.RewriteCount < 0 || *row.RewriteCount > *row.ImageUseCount || (*row.ResourceCount > 0 && size == 0) || (*row.ImageUseCount > 0 && *row.ResourceCount == 0) {
			return invalid
		}
	}
	return nil
}

func runNVKEAgentInstallPlanImmutable(cmd *cobra.Command, ctx context.Context, c *api.Client, id, verb string, sealedTree bool) error {
	reviewedSource, err := reviewNVKEImageBindingSources(cmd, ctx, c, id)
	if err != nil {
		return err
	}
	review, source := reviewedSource.review, reviewedSource.source
	plan, publication, tree := reviewedSource.plan, reviewedSource.publication, reviewedSource.tree
	workload, binding := reviewedSource.workload, reviewedSource.binding
	inputSource, artifactID := reviewedSource.input, reviewedSource.artifactID
	var row *api.NVKEInstallPlanImmutableArtifact
	var limits nvkeImmutableBody
	if verb == "prepare" {
		text, reviewed, e := nvkeInstallPlanImmutableIntent(cmd, c, review, source, plan, publication, tree, workload, binding, artifactID, sealedTree)
		if e != nil {
			return e
		}
		limits = reviewed
		row, err = c.PrepareNVKEInstallPlanImmutable(ctx, api.NVKEInstallPlanImmutablePrepare{NVKEInstallPlanImmutableSource: inputSource, RequestText: text}, sealedTree)
	} else {
		row, err = c.ReviewNVKEInstallPlanImmutable(ctx, api.NVKEInstallPlanImmutableReview{NVKEInstallPlanImmutableSource: inputSource, ArtifactID: artifactID}, sealedTree, verb == "retry-preparation")
	}
	if err != nil {
		return fmt.Errorf("%w; retain --artifact %s and use install-plan %s review before another preparation", err, artifactID, nvkeImmutableName(sealedTree))
	}
	if err = validateNVKEInstallPlanImmutable(row, plan, publication, tree, workload, binding, artifactID, sealedTree); err != nil {
		return err
	}
	if verb == "prepare" {
		size := row.PayloadBytes
		if sealedTree {
			size = row.Bytes
		}
		actual, _ := agentDecimal(size)
		maximum, _ := agentDecimal(limits.MaximumBytes)
		if actual > maximum || (!sealedTree && *row.ResourceCount > *limits.MaximumResources) {
			return errors.New("immutable-artifact response exceeds its reviewed bounds")
		}
	}
	return nvkeJSON(cmd, row)
}

type nvkeReviewedImageBindingSources struct {
	review      api.NVKEAgentJobReview
	source      *api.NVKEOperation
	plan        *api.NVKEInstallPlan
	publication *api.NVKEInstallPlanCharts
	tree        *api.NVKEInstallPlanTree
	workload    *api.NVKEInstallPlanWorkload
	binding     *api.NVKEInstallPlanImageBinding
	input       api.NVKEInstallPlanImmutableSource
	artifactID  string
}

func reviewNVKEImageBindingSources(cmd *cobra.Command, ctx context.Context, c *api.Client, id string) (*nvkeReviewedImageBindingSources, error) {
	planID, artifactID, digest := resourceStringFlag(cmd, "plan"), resourceStringFlag(cmd, "artifact"), resourceStringFlag(cmd, "plan-digest")
	treeID, treeDigest := resourceStringFlag(cmd, "tree"), resourceStringFlag(cmd, "tree-digest")
	treeVersion, _ := cmd.Flags().GetInt("tree-version")
	chartID, chartDigest := resourceStringFlag(cmd, "chart-artifact"), resourceStringFlag(cmd, "chart-artifact-digest")
	version, _ := cmd.Flags().GetInt("plan-version")
	chartVersion, _ := cmd.Flags().GetInt("chart-artifact-version")
	workloadID, workloadDigest := resourceStringFlag(cmd, "workload"), resourceStringFlag(cmd, "workload-digest")
	workloadVersion, _ := cmd.Flags().GetInt("workload-version")
	bindingID, bindingDigest := resourceStringFlag(cmd, "binding"), resourceStringFlag(cmd, "binding-digest")
	bindingVersion, _ := cmd.Flags().GetInt("binding-version")
	if bindingVersion < 1 || !nvkeDigestPattern.MatchString(bindingDigest) {
		return nil, errors.New("review the exact image-binding version and SHA-256")
	}
	identities := map[string]bool{}
	for _, value := range []string{id, planID, chartID, treeID, artifactID, workloadID, bindingID} {
		if nvkeGUID(value) != nil || identities[value] {
			return nil, errors.New("review distinct exact original, plan, chart, tree and workload UUIDs")
		}
		identities[value] = true
	}
	if workloadVersion < 1 || !nvkeDigestPattern.MatchString(workloadDigest) || version < 1 || chartVersion < 1 || treeVersion < 1 || !nvkeDigestPattern.MatchString(treeDigest) || !nvkeDigestPattern.MatchString(digest) || !nvkeDigestPattern.MatchString(chartDigest) {
		return nil, errors.New("review exact plan and chart publication versions and SHA-256 digests")
	}
	review, source, err := nvkeAgentJobReview(cmd, ctx, c, id)
	if err != nil {
		return nil, err
	}
	if source.Status != "UNCERTAIN" || source.OperationKey != "tooling.management.install" {
		return nil, errors.New("immutable artifacts requires the original uncertain management installation")
	}
	selected, err := c.NVKEAgentAuthority(ctx, source.SiteID)
	if err != nil {
		return nil, err
	}
	if err = validateNVKEAgentReply(selected, api.NVKEAgentReview{SiteID: source.SiteID, ExpectedSiteVersion: review.ExpectedSiteVersion, ExpectedVersion: review.ExpectedAuthorityVersion}, "verify"); err != nil {
		return nil, err
	}
	plan, err := c.ReviewNVKEInstallPlan(ctx, api.NVKEInstallPlanReview{NVKEAgentJobReview: review, PlanID: planID}, false)
	if err != nil {
		return nil, err
	}
	if err = validateNVKEInstallPlan(plan, source, review, selected, planID); err != nil {
		return nil, err
	}
	if plan.Version != version || plan.Plan.PlanDigest != digest {
		return nil, errors.New("the prepared plan changed; review before immutable artifacts")
	}
	parent := api.NVKEInstallPlanChartsSource{NVKEInstallPlanReview: api.NVKEInstallPlanReview{NVKEAgentJobReview: review, PlanID: planID}, ExpectedPlanVersion: version, PlanDigest: digest}
	publication, err := c.ReviewNVKEInstallPlanCharts(ctx, api.NVKEInstallPlanChartsReview{NVKEInstallPlanChartsSource: parent, ArtifactID: chartID}, false)
	if err != nil {
		return nil, err
	}
	if err = validateNVKEInstallPlanCharts(publication, plan, chartID, nvkeChartLimits{}); err != nil {
		return nil, err
	}
	if publication.Version != chartVersion || publication.ArtifactDigest != chartDigest {
		return nil, errors.New("the prepared chart publication changed; review before immutable artifacts")
	}
	input := api.NVKEInstallPlanTreeSource{NVKEInstallPlanChartsSource: parent, ChartArtifactID: chartID, ExpectedChartArtifactVersion: chartVersion, ChartArtifactDigest: chartDigest}
	tree, err := c.ReviewNVKEInstallPlanTree(ctx, api.NVKEInstallPlanTreeReview{NVKEInstallPlanTreeSource: input, ArtifactID: treeID}, false)
	if err != nil {
		return nil, err
	}
	if err = validateNVKEInstallPlanTree(tree, plan, publication, treeID, 0); err != nil {
		return nil, err
	}
	if tree.Version != treeVersion || tree.TreeDigest != treeDigest {
		return nil, errors.New("the execution tree changed; review before immutable artifacts")
	}

	workloadSource := api.NVKEInstallPlanWorkloadSource{NVKEInstallPlanTreeSource: input, TreeID: treeID, ExpectedTreeVersion: treeVersion, TreeDigest: treeDigest}
	workload, err := c.ReviewNVKEInstallPlanWorkload(ctx, api.NVKEInstallPlanWorkloadReview{NVKEInstallPlanWorkloadSource: workloadSource, ArtifactID: workloadID}, false)
	if err != nil {
		return nil, err
	}
	if err = validateNVKEInstallPlanWorkload(workload, plan, publication, tree, workloadID); err != nil {
		return nil, err
	}
	if workload.Version != workloadVersion || workload.WorkloadDigest != workloadDigest {
		return nil, errors.New("the workload inventory changed; review before preparing immutable artifacts")
	}
	bindingSource := api.NVKEInstallPlanImageBindingSource{NVKEInstallPlanWorkloadSource: workloadSource, WorkloadID: workloadID, ExpectedWorkloadVersion: workloadVersion, WorkloadDigest: workloadDigest}
	binding, err := c.ReviewNVKEInstallPlanImageBinding(ctx, api.NVKEInstallPlanImageBindingReview{NVKEInstallPlanImageBindingSource: bindingSource, ArtifactID: bindingID}, false)
	if err != nil {
		return nil, err
	}
	if err = validateNVKEInstallPlanImageBinding(binding, plan, publication, tree, workload, bindingID); err != nil {
		return nil, err
	}
	if binding.Version != bindingVersion || binding.BindingDigest != bindingDigest {
		return nil, errors.New("the reviewed image binding changed")
	}
	inputSource := api.NVKEInstallPlanImmutableSource{NVKEInstallPlanImageBindingSource: bindingSource, BindingID: bindingID, ExpectedBindingVersion: bindingVersion, BindingDigest: bindingDigest}
	return &nvkeReviewedImageBindingSources{review: review, source: source, plan: plan, publication: publication, tree: tree, workload: workload, binding: binding, input: inputSource, artifactID: artifactID}, nil
}

// nvkeShortRetry keeps the deepest retry-preparation commands' one-line summary
// short enough for their man page NAME line (80 bytes); Long carries the detail.
func nvkeShortRetry(verb, short string) string {
	if verb == "retry-preparation" {
		return "retry"
	}
	return short
}
