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

const nvkePullSecretAnalysisState = "requires-pod-ownership-kubelet-configuration-plugin-registry-correspondence-and-linked-admission"

func newNVKEAgentInstallPlanPullSecretAnalysisCommand() *cobra.Command {
	name := "pull-secret-analysis"
	group := &cobra.Command{Use: name, Short: "Prepare and recover admitted-Pod Secret-only image credential interpretations"}
	for _, verb := range []string{"prepare", "review", "retry-preparation"} {
		action := verb
		leaf := &cobra.Command{Use: verb + " OPERATION_GUID", Args: cobra.ExactArgs(1), Short: nvkeShortRetry(verb, verb+" a private Secret-only interpretation"), Long: `Preparation saves a private metadata journal before sending the bounded request.
Review reads the retained artifact after a lost response. Explicit retry uses the
server's original private intent and original caller/token. These commands do not
execute the installation or release the original uncertainty fence.
The explicit feature setting describes a Secret-only interpretation, not observed
kubelet configuration or successful registry authorization.`, RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
			return runNVKEAgentInstallPlanPullSecretAnalysis(cmd, ctx, c, args[0], action)
		})}
		nvkeAgentJobFlags(leaf)
		for _, flag := range []string{"plan", "plan-digest", "chart-artifact", "chart-artifact-digest", "tree", "tree-digest", "workload", "workload-digest", "binding", "binding-digest", "artifact", "pull-authority", "pull-authority-digest"} {
			leaf.Flags().String(flag, "", "Exact reviewed "+flag+" (required)")
			_ = leaf.MarkFlagRequired(flag)
		}
		for _, flag := range []string{"plan-version", "chart-artifact-version", "tree-version", "workload-version", "binding-version", "pull-authority-version"} {
			leaf.Flags().Int(flag, 0, "Exact reviewed "+flag+" (required)")
			_ = leaf.MarkFlagRequired(flag)
		}
		if verb == "prepare" {
			leaf.Flags().String("file", "", "Private complete private Secret-only interpretation JSON request or - for stdin (required)")
			leaf.Flags().String("request-file", "", "New or matching private metadata recovery journal (required)")
			_ = leaf.MarkFlagRequired("file")
			_ = leaf.MarkFlagRequired("request-file")
		}
		group.AddCommand(leaf)
	}
	return group
}

type nvkePullSecretAnalysisJournal struct {
	nvkeImmutableJournal
	PullAuthorityID      string `json:"pullAuthorityId"`
	PullAuthorityVersion int    `json:"pullAuthorityVersion"`
	PullAuthorityDigest  string `json:"pullAuthorityDigest"`
}

type nvkePullSecretImageUse struct {
	PodKey                   string `json:"podKey"`
	ImagePointer             string `json:"imagePointer"`
	EnsureSecretPulledImages *bool  `json:"ensureSecretPulledImages"`
}
type nvkePullSecretAnalysisBody struct {
	Format              int                      `json:"format"`
	ArtifactID          string                   `json:"artifactId"`
	PlanID              string                   `json:"planId"`
	PlanDigest          string                   `json:"planDigest"`
	TreeID              string                   `json:"treeId"`
	TreeDigest          string                   `json:"treeDigest"`
	BindingID           string                   `json:"bindingId"`
	BindingDigest       string                   `json:"bindingDigest"`
	PullAuthorityID     string                   `json:"pullAuthorityId"`
	PullAuthorityDigest string                   `json:"pullAuthorityDigest"`
	ImageSelections     []nvkePullSecretImageUse `json:"imageSelections"`
	MaximumBytes        string                   `json:"maximumBytes"`
	MaximumHelperBytes  string                   `json:"maximumHelperBytes"`
	TimeoutSeconds      int                      `json:"timeoutSeconds"`
}

// Go's JSON decoder accepts case aliases. Require the exact serialized field
// names and non-null values as well as duplicate/unknown/trailing-data rejection.
func decodeNVKEPullSecretExact(data []byte, target any) error {
	invalid := errors.New("use exact closed private Secret analysis metadata")
	if rejectNVKEWorkloadDuplicateKeys(data) != nil || decodeNVKEStrict(data, target) != nil {
		return invalid
	}
	var fields, expected map[string]json.RawMessage
	canonical, err := json.Marshal(target)
	if err != nil || json.Unmarshal(data, &fields) != nil || fields == nil || json.Unmarshal(canonical, &expected) != nil || len(fields) != len(expected) {
		return invalid
	}
	for key, value := range fields {
		if expected[key] == nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return invalid
		}
	}
	return nil
}

var nvkePullSecretImagePointer = regexp.MustCompile(`^/spec/(containers|initContainers|ephemeralContainers)/(0|[1-9][0-9]{0,5})/image$`)

func parseNVKEPullSecretAnalysisBody(text []byte) (nvkePullSecretAnalysisBody, error) {
	var body nvkePullSecretAnalysisBody
	invalid := errors.New("use explicit admitted image uses, feature settings and bounded Secret-only interpretation")
	if len(text) > 1048576 || decodeNVKEPullSecretExact(text, &body) != nil || body.Format != 1 {
		return body, invalid
	}
	maximum, err := agentDecimal(body.MaximumBytes)
	helper, helperErr := agentDecimal(body.MaximumHelperBytes)
	if err != nil || helperErr != nil || maximum < 1 || maximum > 67108864 || helper < 1 || helper > maximum || body.TimeoutSeconds < 1 || body.TimeoutSeconds > 300 || len(body.ImageSelections) < 1 || len(body.ImageSelections) > 1024 {
		return body, invalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(text, &fields) != nil {
		return body, invalid
	}
	var selected []json.RawMessage
	if json.Unmarshal(fields["imageSelections"], &selected) != nil {
		return body, invalid
	}
	seen := map[string]bool{}
	for i, row := range body.ImageSelections {
		var exact nvkePullSecretImageUse
		if decodeNVKEPullSecretExact(selected[i], &exact) != nil || row.EnsureSecretPulledImages == nil || strings.Count(row.PodKey, "/") != 1 || !nvkePullSecretImagePointer.MatchString(row.ImagePointer) {
			return body, invalid
		}
		parts := strings.Split(row.PodKey, "/")
		if len(parts[0]) > 63 || strings.Contains(parts[0], ".") || !nvkeNodeName.MatchString(parts[0]) || len(parts[1]) > 253 || !nvkeNodeName.MatchString(parts[1]) {
			return body, invalid
		}
		key := row.PodKey + "\x00" + row.ImagePointer
		if seen[key] {
			return body, invalid
		}
		seen[key] = true
	}
	return body, nil
}

func nvkeInstallPlanPullSecretAnalysisIntent(cmd *cobra.Command, c *api.Client, sources *nvkeReviewedImageBindingSources, parent *api.NVKEInstallPlanPullAuthority) (string, nvkePullSecretAnalysisBody, error) {
	var body nvkePullSecretAnalysisBody
	file, journal := resourceStringFlag(cmd, "file"), resourceStringFlag(cmd, "request-file")
	if file == "" || journal == "" || journal == "-" {
		return "", body, errors.New("use a private input and a private recovery journal path")
	}
	text, err := readNVKEText(cmd, file)
	if err != nil {
		return "", body, err
	}
	body, err = parseNVKEPullSecretAnalysisBody(text)
	review, source := sources.review, sources.source
	plan, publication, executionTree := sources.plan, sources.publication, sources.tree
	workload, binding, artifactID := sources.workload, sources.binding, sources.artifactID
	if err != nil {
		return "", body, err
	}
	if body.ArtifactID != artifactID || body.PlanID != plan.ID || body.PlanDigest != plan.Plan.PlanDigest || body.TreeID != executionTree.ID || body.TreeDigest != executionTree.TreeDigest || body.BindingID != binding.ID || body.BindingDigest != binding.BindingDigest || body.PullAuthorityID != parent.ID || body.PullAuthorityDigest != parent.AuthorityDigest {
		return "", body, errors.New("private Secret-only interpretation input does not match the exact reviewed original source")
	}
	saved := nvkePullSecretAnalysisJournal{nvkeImmutableJournal: nvkeImmutableJournal{nvkeImageBindingJournal: nvkeImageBindingJournal{WorkloadID: workload.ID, WorkloadVersion: workload.Version, WorkloadDigest: workload.WorkloadDigest, nvkeChartJournal: nvkeChartJournal{nvkePlanJournal: nvkePlanJournal{Format: 1, Kind: "nvke-install-plan-pull-secret-analysis", Server: c.BaseURL(), OrganizationID: c.Org(), ActorID: review.ExpectedActorID, SiteID: source.SiteID, SiteVersion: review.ExpectedSiteVersion, AuthorityVersion: review.ExpectedAuthorityVersion, OperationID: source.ID, OperationVersion: review.ExpectedOperationVersion, PlanID: plan.ID, InputSHA256: nvkeParameterDigest(text)}, PlanVersion: plan.Version, PlanDigest: plan.Plan.PlanDigest, ArtifactID: artifactID}, ChartArtifactID: publication.ID, ChartArtifactVersion: publication.Version, ChartArtifactDigest: publication.ArtifactDigest, TreeID: executionTree.ID, TreeVersion: executionTree.Version, TreeDigest: executionTree.TreeDigest}, BindingID: binding.ID, BindingVersion: binding.Version, BindingDigest: binding.BindingDigest}, PullAuthorityID: parent.ID, PullAuthorityVersion: parent.Version, PullAuthorityDigest: parent.AuthorityDigest}
	_, err = privatefile.ReadFile(journal, 16384)
	if errors.Is(err, os.ErrNotExist) {
		data, e := json.MarshalIndent(saved, "", "  ")
		if e != nil {
			return "", body, errors.New("private Secret-only interpretation recovery metadata unavailable")
		}
		if e = writeNVKEExclusive(journal, append(data, '\n')); e != nil {
			return "", body, e
		}
	} else if err != nil {
		return "", body, errors.New("preserve the original private Secret-only interpretation journal")
	}
	data, err := privatefile.ReadFile(journal, 16384)
	var retained nvkePullSecretAnalysisJournal
	if err != nil || decodeNVKEPullSecretExact(data, &retained) != nil || !reflect.DeepEqual(saved, retained) {
		return "", body, errors.New("private Secret-only interpretation source or exact private input changed; preserve the original artifact and journal")
	}
	return string(text), body, nil
}

func validateNVKEInstallPlanPullSecretAnalysis(row *api.NVKEInstallPlanPullSecretAnalysis, plan *api.NVKEInstallPlan, publication *api.NVKEInstallPlanCharts, executionTree *api.NVKEInstallPlanTree, workload *api.NVKEInstallPlanWorkload, binding *api.NVKEInstallPlanImageBinding, parent *api.NVKEInstallPlanPullAuthority, artifactID string) error {
	invalid := errors.New("private Secret-only interpretation did not confirm its exact reviewed source and unresolved proof state")
	if row == nil || row.ID != artifactID || row.Version < 1 || row.Status != "prepared" || row.PlanID != plan.ID || row.PlanVersion != plan.Version || row.PlanDigest != plan.Plan.PlanDigest || row.ChartArtifactID != publication.ID || row.ChartArtifactVersion != publication.Version || row.ChartArtifactDigest != publication.ArtifactDigest || row.TreeID != executionTree.ID || row.TreeVersion != executionTree.Version || row.TreeDigest != executionTree.TreeDigest || row.WorkloadID != workload.ID || row.WorkloadVersion != workload.Version || row.WorkloadDigest != workload.WorkloadDigest || row.BindingID != binding.ID || row.BindingVersion != binding.Version || row.BindingDigest != binding.BindingDigest || row.PullAuthorityID != parent.ID || row.PullAuthorityVersion != parent.Version || row.PullAuthorityDigest != parent.AuthorityDigest || row.OperationID != plan.OperationID || row.OperationVersion != plan.OperationVersion || row.SiteID != plan.SiteID || row.SiteVersion != plan.SiteVersion || row.AuthorityID != plan.AuthorityID || row.AuthorityVersion != plan.AuthorityVersion || row.ContinuationAuthorized == nil || *row.ContinuationAuthorized {
		return invalid
	}
	for _, digest := range []string{row.ArtifactDigest, row.SourceDigest, row.CaptureDigest, row.AuthorityDigest} {
		if !nvkeDigestPattern.MatchString(digest) {
			return invalid
		}
	}
	sourceTime, sourceErr := time.Parse(time.RFC3339Nano, row.SourceObservedAt)
	parentTime, parentErr := time.Parse(time.RFC3339Nano, parent.ObservedAt)
	bindingTime, bindingErr := time.Parse(time.RFC3339Nano, binding.ObservedAt)
	analyzed, err := time.Parse(time.RFC3339Nano, row.AnalyzedAt)
	if err != nil || sourceErr != nil || parentErr != nil || bindingErr != nil || sourceTime.Before(bindingTime) || sourceTime.After(parentTime) || analyzed.Before(parentTime) || row.ExecutionState != nvkePullSecretAnalysisState || row.ImageUseCount == nil || *row.ImageUseCount < 1 || *row.ImageUseCount > 1024 || row.CandidateCount == nil || *row.CandidateCount < 0 || *row.CandidateCount > *row.ImageUseCount*100000 || row.MissingReferenceCount == nil || *row.MissingReferenceCount < 0 || *row.MissingReferenceCount > *row.ImageUseCount*10000 {
		return invalid
	}

	return nil
}

func runNVKEAgentInstallPlanPullSecretAnalysis(cmd *cobra.Command, ctx context.Context, c *api.Client, id, verb string) error {
	parentID, parentDigest := resourceStringFlag(cmd, "pull-authority"), resourceStringFlag(cmd, "pull-authority-digest")
	parentVersion, _ := cmd.Flags().GetInt("pull-authority-version")
	seen := map[string]bool{}
	for _, value := range []string{id, resourceStringFlag(cmd, "plan"), resourceStringFlag(cmd, "chart-artifact"), resourceStringFlag(cmd, "tree"), resourceStringFlag(cmd, "workload"), resourceStringFlag(cmd, "binding"), resourceStringFlag(cmd, "artifact"), parentID} {
		if nvkeGUID(value) != nil || seen[value] {
			return errors.New("review distinct original, artifact and pull-observation UUIDs")
		}
		seen[value] = true
	}
	if parentVersion < 1 || !nvkeDigestPattern.MatchString(parentDigest) {
		return errors.New("review the exact pull-observation version and digest")
	}
	sources, err := reviewNVKEImageBindingSources(cmd, ctx, c, id)
	if err != nil {
		return err
	}
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
	input := api.NVKEInstallPlanPullSecretAnalysisSource{NVKEInstallPlanImmutableSource: sources.input, PullAuthorityID: parentID, ExpectedPullAuthorityVersion: parentVersion, PullAuthorityDigest: parentDigest}

	var row *api.NVKEInstallPlanPullSecretAnalysis
	var limits nvkePullSecretAnalysisBody
	if verb == "prepare" {
		text, body, e := nvkeInstallPlanPullSecretAnalysisIntent(cmd, c, sources, parent)
		if e != nil {
			return e
		}
		limits = body
		row, err = c.PrepareNVKEInstallPlanPullSecretAnalysis(ctx, api.NVKEInstallPlanPullSecretAnalysisPrepare{NVKEInstallPlanPullSecretAnalysisSource: input, RequestText: text})
	} else {
		row, err = c.ReviewNVKEInstallPlanPullSecretAnalysis(ctx, api.NVKEInstallPlanPullSecretAnalysisReview{NVKEInstallPlanPullSecretAnalysisSource: input, ArtifactID: sources.artifactID}, verb == "retry-preparation")
	}
	if err != nil {
		return fmt.Errorf("%w; retain --artifact %s and review pull-secret-analysis before another preparation", err, sources.artifactID)
	}
	if err = validateNVKEInstallPlanPullSecretAnalysis(row, sources.plan, sources.publication, sources.tree, sources.workload, sources.binding, parent, sources.artifactID); err != nil {
		return err
	}
	if verb == "prepare" && *row.ImageUseCount != len(limits.ImageSelections) {
		return errors.New("secret-only interpretation result exceeds its reviewed image-use scope")
	}

	return nvkeJSON(cmd, row)
}
