package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/spf13/cobra"
)

const nvkePullAuthorityState = "requires-pod-ownership-kubelet-credential-registry-correspondence-and-linked-admission"

func newNVKEAgentInstallPlanPullAuthorityCommand() *cobra.Command {
	name := "pull-authority"
	group := &cobra.Command{Use: name, Short: "Prepare and recover admitted-Pod pull authority observations"}
	for _, verb := range []string{"prepare", "review", "retry-preparation"} {
		action := verb
		leaf := &cobra.Command{Use: verb + " OPERATION_GUID", Args: cobra.ExactArgs(1), Short: verb + " a admitted-Pod pull authority observation", Long: `Preparation saves a private metadata journal before sending the bounded request.
Review reads the retained artifact after a lost response. Explicit retry uses the
server's original private intent and original caller/token. These commands do not
execute the installation or release the original uncertainty fence.`, RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
			return runNVKEAgentInstallPlanPullAuthority(cmd, ctx, c, args[0], action)
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
			leaf.Flags().String("file", "", "Private complete admitted-Pod pull authority JSON request or - for stdin (required)")
			leaf.Flags().String("request-file", "", "New or matching private metadata recovery journal (required)")
			_ = leaf.MarkFlagRequired("file")
			_ = leaf.MarkFlagRequired("request-file")
		}
		group.AddCommand(leaf)
	}
	return group
}

type nvkePullAuthorityBody struct {
	Format         int               `json:"format"`
	ArtifactID     string            `json:"artifactId"`
	PlanID         string            `json:"planId"`
	PlanDigest     string            `json:"planDigest"`
	TreeID         string            `json:"treeId"`
	TreeDigest     string            `json:"treeDigest"`
	BindingID      string            `json:"bindingId"`
	BindingDigest  string            `json:"bindingDigest"`
	SelectedPods   map[string]string `json:"selectedPods"`
	MaximumBytes   string            `json:"maximumBytes"`
	MaximumCalls   int               `json:"maximumCalls"`
	TimeoutSeconds int               `json:"timeoutSeconds"`
}

func parseNVKEPullAuthorityBody(text []byte) (nvkePullAuthorityBody, error) {
	var body nvkePullAuthorityBody
	invalid := errors.New("use an explicit bounded namespace/Pod scope with exact live Pod UIDs")
	if len(text) > 1024*1024 || rejectNVKEWorkloadDuplicateKeys(text) != nil || decodeNVKEStrict(text, &body) != nil {
		return body, invalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(text, &fields) != nil {
		return body, invalid
	}
	required := []string{"format", "artifactId", "planId", "planDigest", "treeId", "treeDigest", "bindingId", "bindingDigest", "selectedPods", "maximumBytes", "maximumCalls", "timeoutSeconds"}
	if len(fields) != len(required) || body.Format != 1 {
		return body, invalid
	}
	for _, name := range required {
		if fields[name] == nil || bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return body, invalid
		}
	}
	size, err := agentDecimal(body.MaximumBytes)
	if err != nil || size < 1 || size > 64*1024*1024 || len(body.SelectedPods) < 1 || len(body.SelectedPods) > 10000 || body.MaximumCalls < 2*len(body.SelectedPods)+2 || body.MaximumCalls > 32768 || body.TimeoutSeconds < 1 || body.TimeoutSeconds > 300 {
		return body, invalid
	}
	uids := map[string]bool{}
	for key, uid := range body.SelectedPods {
		if strings.Count(key, "/") != 1 {
			return body, invalid
		}
		parts := strings.Split(key, "/")
		if len(parts[0]) > 63 || strings.Contains(parts[0], ".") || !nvkeNodeName.MatchString(parts[0]) || len(parts[1]) > 253 || !nvkeNodeName.MatchString(parts[1]) || !nvkeNodeUID.MatchString(uid) || uids[uid] {
			return body, invalid
		}
		uids[uid] = true
	}
	return body, nil
}

func nvkeInstallPlanPullAuthorityIntent(cmd *cobra.Command, c *api.Client, sources *nvkeReviewedImageBindingSources) (string, nvkePullAuthorityBody, error) {
	var body nvkePullAuthorityBody
	file, journal := resourceStringFlag(cmd, "file"), resourceStringFlag(cmd, "request-file")
	if file == "" || journal == "" || journal == "-" {
		return "", body, errors.New("use a private input and a private recovery journal path")
	}
	text, err := readNVKEText(cmd, file)
	if err != nil {
		return "", body, err
	}
	body, err = parseNVKEPullAuthorityBody(text)
	review, source := sources.review, sources.source
	plan, publication, executionTree := sources.plan, sources.publication, sources.tree
	workload, binding, artifactID := sources.workload, sources.binding, sources.artifactID
	if err != nil {
		return "", body, err
	}
	if body.ArtifactID != artifactID || body.PlanID != plan.ID || body.PlanDigest != plan.Plan.PlanDigest || body.TreeID != executionTree.ID || body.TreeDigest != executionTree.TreeDigest || body.BindingID != binding.ID || body.BindingDigest != binding.BindingDigest {
		return "", body, errors.New("admitted-Pod pull authority input does not match the exact reviewed original source")
	}
	saved := nvkeImmutableJournal{nvkeImageBindingJournal: nvkeImageBindingJournal{WorkloadID: workload.ID, WorkloadVersion: workload.Version, WorkloadDigest: workload.WorkloadDigest, nvkeChartJournal: nvkeChartJournal{nvkePlanJournal: nvkePlanJournal{Format: 1, Kind: "nvke-install-plan-pull-authority", Server: c.BaseURL(), OrganizationID: c.Org(), ActorID: review.ExpectedActorID, SiteID: source.SiteID, SiteVersion: review.ExpectedSiteVersion, AuthorityVersion: review.ExpectedAuthorityVersion, OperationID: source.ID, OperationVersion: review.ExpectedOperationVersion, PlanID: plan.ID, InputSHA256: nvkeParameterDigest(text)}, PlanVersion: plan.Version, PlanDigest: plan.Plan.PlanDigest, ArtifactID: artifactID}, ChartArtifactID: publication.ID, ChartArtifactVersion: publication.Version, ChartArtifactDigest: publication.ArtifactDigest, TreeID: executionTree.ID, TreeVersion: executionTree.Version, TreeDigest: executionTree.TreeDigest}, BindingID: binding.ID, BindingVersion: binding.Version, BindingDigest: binding.BindingDigest}
	_, err = privatefile.ReadFile(journal, 16384)
	if errors.Is(err, os.ErrNotExist) {
		data, e := json.MarshalIndent(saved, "", "  ")
		if e != nil {
			return "", body, errors.New("admitted-Pod pull authority recovery metadata unavailable")
		}
		if e = writeNVKEExclusive(journal, append(data, '\n')); e != nil {
			return "", body, e
		}
	} else if err != nil {
		return "", body, errors.New("preserve the original private admitted-Pod pull authority journal")
	}
	data, err := privatefile.ReadFile(journal, 16384)
	var retained nvkeImmutableJournal
	if err != nil || decodeNVKEImmutableStrict(data, &retained) != nil || !reflect.DeepEqual(saved, retained) {
		return "", body, errors.New("admitted-Pod pull authority source or exact private input changed; preserve the original artifact and journal")
	}
	return string(text), body, nil
}

func validateNVKEInstallPlanPullAuthority(row *api.NVKEInstallPlanPullAuthority, plan *api.NVKEInstallPlan, publication *api.NVKEInstallPlanCharts, executionTree *api.NVKEInstallPlanTree, workload *api.NVKEInstallPlanWorkload, binding *api.NVKEInstallPlanImageBinding, artifactID string) error {
	invalid := errors.New("admitted-Pod pull authority observation did not confirm its exact reviewed source and unresolved proof state")
	if row == nil || row.ID != artifactID || row.Version < 1 || row.Status != "prepared" || row.PlanID != plan.ID || row.PlanVersion != plan.Version || row.PlanDigest != plan.Plan.PlanDigest || row.ChartArtifactID != publication.ID || row.ChartArtifactVersion != publication.Version || row.ChartArtifactDigest != publication.ArtifactDigest || row.TreeID != executionTree.ID || row.TreeVersion != executionTree.Version || row.TreeDigest != executionTree.TreeDigest || row.WorkloadID != workload.ID || row.WorkloadVersion != workload.Version || row.WorkloadDigest != workload.WorkloadDigest || row.BindingID != binding.ID || row.BindingVersion != binding.Version || row.BindingDigest != binding.BindingDigest || row.OperationID != plan.OperationID || row.OperationVersion != plan.OperationVersion || row.SiteID != plan.SiteID || row.SiteVersion != plan.SiteVersion || row.AuthorityID != plan.AuthorityID || row.AuthorityVersion != plan.AuthorityVersion || row.ContinuationAuthorized == nil || *row.ContinuationAuthorized {
		return invalid
	}
	for _, digest := range []string{row.ArtifactDigest, row.SourceDigest, row.CaptureDigest, row.AuthorityDigest} {
		if !nvkeDigestPattern.MatchString(digest) {
			return invalid
		}
	}
	observed, err := time.Parse(time.RFC3339Nano, row.ObservedAt)
	parent, parentErr := time.Parse(time.RFC3339Nano, binding.ObservedAt)
	if err != nil || parentErr != nil || observed.Before(parent) || row.ExecutionState != nvkePullAuthorityState || row.PodCount == nil || *row.PodCount < 1 || *row.PodCount > 10000 || row.ServiceAccountCount == nil || *row.ServiceAccountCount < 0 || *row.ServiceAccountCount > *row.PodCount || row.SecretCount == nil || *row.SecretCount < 0 || *row.SecretCount > 32768 || row.MissingReferenceCount == nil || *row.MissingReferenceCount < 0 || *row.MissingReferenceCount > *row.ServiceAccountCount+*row.SecretCount {
		return invalid
	}
	if 2*(int64(*row.PodCount)+int64(*row.ServiceAccountCount)+int64(*row.SecretCount))+2 > 32768 {
		return invalid
	}
	return nil
}

func runNVKEAgentInstallPlanPullAuthority(cmd *cobra.Command, ctx context.Context, c *api.Client, id, verb string) error {
	sources, err := reviewNVKEImageBindingSources(cmd, ctx, c, id)
	if err != nil {
		return err
	}
	var row *api.NVKEInstallPlanPullAuthority
	var limits nvkePullAuthorityBody
	if verb == "prepare" {
		text, body, e := nvkeInstallPlanPullAuthorityIntent(cmd, c, sources)
		if e != nil {
			return e
		}
		limits = body
		row, err = c.PrepareNVKEInstallPlanPullAuthority(ctx, api.NVKEInstallPlanPullAuthorityPrepare{NVKEInstallPlanImmutableSource: sources.input, RequestText: text})
	} else {
		row, err = c.ReviewNVKEInstallPlanPullAuthority(ctx, api.NVKEInstallPlanPullAuthorityReview{NVKEInstallPlanImmutableSource: sources.input, ArtifactID: sources.artifactID}, verb == "retry-preparation")
	}
	if err != nil {
		return fmt.Errorf("%w; retain --artifact %s and review pull-authority before another preparation", err, sources.artifactID)
	}
	if err = validateNVKEInstallPlanPullAuthority(row, sources.plan, sources.publication, sources.tree, sources.workload, sources.binding, sources.artifactID); err != nil {
		return err
	}
	if verb == "prepare" && (*row.PodCount != len(limits.SelectedPods) || 2*(*row.PodCount+*row.ServiceAccountCount+*row.SecretCount)+2 > limits.MaximumCalls) {
		return errors.New("admitted-Pod pull authority result exceeds its reviewed Pod scope or read-call budget")
	}
	return nvkeJSON(cmd, row)
}
