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
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/spf13/cobra"
)

const nvkeNodePlatformsState = "requires-attested-cpu-importer-scheduling-credentials-availability-phase-use-and-linked-admission"

func newNVKEAgentInstallPlanNodePlatformsCommand() *cobra.Command {
	name := "node-platforms"
	group := &cobra.Command{Use: name, Short: "Prepare and recover node/image platform observations"}
	for _, verb := range []string{"prepare", "review", "retry-preparation"} {
		action := verb
		leaf := &cobra.Command{Use: verb + " OPERATION_GUID", Args: cobra.ExactArgs(1), Short: verb + " a node/image platform observation", Long: `Preparation saves a private metadata journal before sending the bounded request.
Review reads the retained artifact after a lost response. Explicit retry uses the
server's original private intent and original caller/token. These commands do not
execute the installation or release the original uncertainty fence.`, RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
			return runNVKEAgentInstallPlanNodePlatforms(cmd, ctx, c, args[0], action)
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
			leaf.Flags().String("file", "", "Private complete node/platform JSON request or - for stdin (required)")
			leaf.Flags().String("request-file", "", "New or matching private metadata recovery journal (required)")
			_ = leaf.MarkFlagRequired("file")
			_ = leaf.MarkFlagRequired("request-file")
		}
		group.AddCommand(leaf)
	}
	return group
}

type nvkeNodePlatform struct {
	UID            string `json:"uid"`
	RuntimeProfile string `json:"runtimeProfile"`
	Platform       struct {
		OS           string   `json:"os"`
		Architecture string   `json:"architecture"`
		Variant      string   `json:"variant"`
		OSVersion    string   `json:"os.version"`
		OSFeatures   []string `json:"os.features"`
	} `json:"platform"`
}

type nvkeNodePlatformsBody struct {
	Format                  int                         `json:"format"`
	ArtifactID              string                      `json:"artifactId"`
	PlanID                  string                      `json:"planId"`
	PlanDigest              string                      `json:"planDigest"`
	TreeID                  string                      `json:"treeId"`
	TreeDigest              string                      `json:"treeDigest"`
	BindingID               string                      `json:"bindingId"`
	BindingDigest           string                      `json:"bindingDigest"`
	NodePlatforms           map[string]nvkeNodePlatform `json:"nodePlatforms"`
	MaximumImageBytes       string                      `json:"maximumImageBytes"`
	MaximumMetadataBytes    string                      `json:"maximumMetadataBytes"`
	MaximumBytes            string                      `json:"maximumBytes"`
	MaximumObservationBytes string                      `json:"maximumObservationBytes"`
	MaximumHelperBytes      string                      `json:"maximumHelperBytes"`
	MaximumCalls            int                         `json:"maximumCalls"`
	MaximumPairs            int                         `json:"maximumPairs"`
	TimeoutSeconds          int                         `json:"timeoutSeconds"`
}

var nvkeNodeName = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?(?:\.[a-z0-9](?:[-a-z0-9]*[a-z0-9])?)*$`)
var nvkeNodeUID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func parseNVKENodePlatformsBody(text []byte) (nvkeNodePlatformsBody, error) {
	var body nvkeNodePlatformsBody
	invalid := errors.New("use an explicit bounded node/platform scope with exact node UIDs and supported runtime profiles")
	if len(text) > 1024*1024 || rejectNVKEWorkloadDuplicateKeys(text) != nil || decodeNVKEStrict(text, &body) != nil {
		return body, invalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(text, &fields) != nil {
		return body, invalid
	}
	required := []string{"format", "artifactId", "planId", "planDigest", "treeId", "treeDigest", "bindingId", "bindingDigest", "nodePlatforms", "maximumImageBytes", "maximumMetadataBytes", "maximumBytes", "maximumObservationBytes", "maximumHelperBytes", "maximumCalls", "maximumPairs", "timeoutSeconds"}
	if len(fields) != len(required) || body.Format != 1 {
		return body, invalid
	}
	for _, name := range required {
		if fields[name] == nil || bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return body, invalid
		}
	}
	for _, limit := range []struct {
		value   string
		maximum int64
	}{{body.MaximumImageBytes, 9223372036854775807}, {body.MaximumMetadataBytes, 64 * 1024 * 1024}, {body.MaximumBytes, 16 * 1024 * 1024}, {body.MaximumObservationBytes, 64 * 1024 * 1024}, {body.MaximumHelperBytes, 9223372036854775807}} {
		value, err := agentDecimal(limit.value)
		if err != nil || value < 1 || value > limit.maximum {
			return body, invalid
		}
	}
	if len(body.NodePlatforms) < 1 || len(body.NodePlatforms) > 10000 || body.MaximumCalls < 4*len(body.NodePlatforms)+4 || body.MaximumCalls > 65536 || body.MaximumPairs < 1 || body.MaximumPairs > 100000 || body.TimeoutSeconds < 1 || body.TimeoutSeconds > 300 {
		return body, invalid
	}
	var rows map[string]map[string]json.RawMessage
	if json.Unmarshal(fields["nodePlatforms"], &rows) != nil {
		return body, invalid
	}
	profiles := map[string]bool{"containerd/v2.3.0": true, "containerd/v2.3.7": true, "containerd/v2.2.3": true, "containerd/v2.1.7": true, "containerd/v1.7.31": true}
	uids := map[string]bool{}
	for name, row := range rows {
		selected := body.NodePlatforms[name]
		if len(name) > 253 || !nvkeNodeName.MatchString(name) || len(row) != 3 || row["uid"] == nil || row["runtimeProfile"] == nil || row["platform"] == nil || !nvkeNodeUID.MatchString(selected.UID) || uids[selected.UID] || !profiles[selected.RuntimeProfile] {
			return body, invalid
		}
		uids[selected.UID] = true
		var platform map[string]json.RawMessage
		if json.Unmarshal(row["platform"], &platform) != nil || len(platform) < 2 || len(platform) > 5 || platform["os"] == nil || platform["architecture"] == nil || selected.Platform.OS == "" || selected.Platform.Architecture == "" {
			return body, invalid
		}
		for key, value := range platform {
			if (key != "os" && key != "architecture" && key != "variant" && key != "os.version" && key != "os.features") || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return body, invalid
			}
		}
		if raw, ok := platform["os.features"]; ok {
			var features []json.RawMessage
			if json.Unmarshal(raw, &features) != nil {
				return body, invalid
			}
			for _, feature := range features {
				if bytes.Equal(bytes.TrimSpace(feature), []byte("null")) {
					return body, invalid
				}
			}
		}
	}
	return body, nil
}

func nvkeInstallPlanNodePlatformsIntent(cmd *cobra.Command, c *api.Client, sources *nvkeReviewedImageBindingSources) (string, nvkeNodePlatformsBody, error) {
	var body nvkeNodePlatformsBody
	file, journal := resourceStringFlag(cmd, "file"), resourceStringFlag(cmd, "request-file")
	if file == "" || journal == "" || journal == "-" {
		return "", body, errors.New("use a private input and a private recovery journal path")
	}
	text, err := readNVKEText(cmd, file)
	if err != nil {
		return "", body, err
	}
	body, err = parseNVKENodePlatformsBody(text)
	review, source := sources.review, sources.source
	plan, publication, executionTree := sources.plan, sources.publication, sources.tree
	workload, binding, artifactID := sources.workload, sources.binding, sources.artifactID
	if err != nil {
		return "", body, err
	}
	if body.ArtifactID != artifactID || body.PlanID != plan.ID || body.PlanDigest != plan.Plan.PlanDigest || body.TreeID != executionTree.ID || body.TreeDigest != executionTree.TreeDigest || body.BindingID != binding.ID || body.BindingDigest != binding.BindingDigest {
		return "", body, errors.New("node/platform input does not match the exact reviewed original source")
	}
	saved := nvkeImmutableJournal{nvkeImageBindingJournal: nvkeImageBindingJournal{WorkloadID: workload.ID, WorkloadVersion: workload.Version, WorkloadDigest: workload.WorkloadDigest, nvkeChartJournal: nvkeChartJournal{nvkePlanJournal: nvkePlanJournal{Format: 1, Kind: "nvke-install-plan-node-platforms", Server: c.BaseURL(), OrganizationID: c.Org(), ActorID: review.ExpectedActorID, SiteID: source.SiteID, SiteVersion: review.ExpectedSiteVersion, AuthorityVersion: review.ExpectedAuthorityVersion, OperationID: source.ID, OperationVersion: review.ExpectedOperationVersion, PlanID: plan.ID, InputSHA256: nvkeParameterDigest(text)}, PlanVersion: plan.Version, PlanDigest: plan.Plan.PlanDigest, ArtifactID: artifactID}, ChartArtifactID: publication.ID, ChartArtifactVersion: publication.Version, ChartArtifactDigest: publication.ArtifactDigest, TreeID: executionTree.ID, TreeVersion: executionTree.Version, TreeDigest: executionTree.TreeDigest}, BindingID: binding.ID, BindingVersion: binding.Version, BindingDigest: binding.BindingDigest}
	_, err = privatefile.ReadFile(journal, 16384)
	if errors.Is(err, os.ErrNotExist) {
		data, e := json.MarshalIndent(saved, "", "  ")
		if e != nil {
			return "", body, errors.New("node/platform recovery metadata unavailable")
		}
		if e = writeNVKEExclusive(journal, append(data, '\n')); e != nil {
			return "", body, e
		}
	} else if err != nil {
		return "", body, errors.New("preserve the original private node/platform journal")
	}
	data, err := privatefile.ReadFile(journal, 16384)
	var retained nvkeImmutableJournal
	if err != nil || decodeNVKEImmutableStrict(data, &retained) != nil || !reflect.DeepEqual(saved, retained) {
		return "", body, errors.New("node/platform source or exact private input changed; preserve the original artifact and journal")
	}
	return string(text), body, nil
}

func validateNVKEInstallPlanNodePlatforms(row *api.NVKEInstallPlanNodePlatforms, plan *api.NVKEInstallPlan, publication *api.NVKEInstallPlanCharts, executionTree *api.NVKEInstallPlanTree, workload *api.NVKEInstallPlanWorkload, binding *api.NVKEInstallPlanImageBinding, artifactID string) error {
	invalid := errors.New("node/platform observation did not confirm its exact reviewed source and unresolved proof state")
	if row == nil || row.ID != artifactID || row.Version < 1 || row.Status != "prepared" || row.PlanID != plan.ID || row.PlanVersion != plan.Version || row.PlanDigest != plan.Plan.PlanDigest || row.ChartArtifactID != publication.ID || row.ChartArtifactVersion != publication.Version || row.ChartArtifactDigest != publication.ArtifactDigest || row.TreeID != executionTree.ID || row.TreeVersion != executionTree.Version || row.TreeDigest != executionTree.TreeDigest || row.WorkloadID != workload.ID || row.WorkloadVersion != workload.Version || row.WorkloadDigest != workload.WorkloadDigest || row.BindingID != binding.ID || row.BindingVersion != binding.Version || row.BindingDigest != binding.BindingDigest || row.OperationID != plan.OperationID || row.OperationVersion != plan.OperationVersion || row.SiteID != plan.SiteID || row.SiteVersion != plan.SiteVersion || row.AuthorityID != plan.AuthorityID || row.AuthorityVersion != plan.AuthorityVersion || row.ContinuationAuthorized == nil || *row.ContinuationAuthorized {
		return invalid
	}
	for _, digest := range []string{row.ArtifactDigest, row.SourceDigest, row.CaptureDigest, row.PlatformDigest} {
		if !nvkeDigestPattern.MatchString(digest) {
			return invalid
		}
	}
	observed, err := time.Parse(time.RFC3339Nano, row.ObservedAt)
	parent, parentErr := time.Parse(time.RFC3339Nano, binding.ObservedAt)
	if err != nil || parentErr != nil || observed.Before(parent) || row.ExecutionState != nvkeNodePlatformsState || row.NodeCount == nil || *row.NodeCount < 1 || *row.NodeCount > 10000 || row.ContentCount == nil || *row.ContentCount < 0 || row.ImageReferenceCount == nil || binding.ImageCount == nil || *row.ImageReferenceCount != *binding.ImageCount || *row.ContentCount > *row.ImageReferenceCount || row.PairCount == nil || *row.PairCount < 0 || *row.PairCount > 100000 || int64(*row.PairCount) != int64(*row.NodeCount)*int64(*row.ContentCount) || row.IssueCount == nil || *row.IssueCount < 0 {
		return invalid
	}
	return nil
}

func runNVKEAgentInstallPlanNodePlatforms(cmd *cobra.Command, ctx context.Context, c *api.Client, id, verb string) error {
	sources, err := reviewNVKEImageBindingSources(cmd, ctx, c, id)
	if err != nil {
		return err
	}
	var row *api.NVKEInstallPlanNodePlatforms
	var limits nvkeNodePlatformsBody
	if verb == "prepare" {
		text, body, e := nvkeInstallPlanNodePlatformsIntent(cmd, c, sources)
		if e != nil {
			return e
		}
		limits = body
		row, err = c.PrepareNVKEInstallPlanNodePlatforms(ctx, api.NVKEInstallPlanNodePlatformsPrepare{NVKEInstallPlanImmutableSource: sources.input, RequestText: text})
	} else {
		row, err = c.ReviewNVKEInstallPlanNodePlatforms(ctx, api.NVKEInstallPlanNodePlatformsReview{NVKEInstallPlanImmutableSource: sources.input, ArtifactID: sources.artifactID}, verb == "retry-preparation")
	}
	if err != nil {
		return fmt.Errorf("%w; retain --artifact %s and review node-platforms before another preparation", err, sources.artifactID)
	}
	if err = validateNVKEInstallPlanNodePlatforms(row, sources.plan, sources.publication, sources.tree, sources.workload, sources.binding, sources.artifactID); err != nil {
		return err
	}
	if verb == "prepare" && (*row.NodeCount != len(limits.NodePlatforms) || *row.PairCount > limits.MaximumPairs) {
		return errors.New("node/platform result exceeds its reviewed node scope or pair budget")
	}
	return nvkeJSON(cmd, row)
}
