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

func newNVKEAgentInstallPlanChartsCommand() *cobra.Command {
	group := &cobra.Command{Use: "charts", Short: "Prepare and recover frozen chart artifacts without executing an install"}
	for _, verb := range []string{"prepare", "review", "retry-preparation"} {
		action := verb
		leaf := &cobra.Command{Use: verb + " OPERATION_GUID", Args: cobra.ExactArgs(1),
			Short: verb + " chart artifacts for the exact reviewed plan",
			Long: `Chart preparation records an immutable metadata-only recovery journal before its
private API request. Review recovers the retained artifact result without fetching
charts again. retry-preparation explicitly repeats the server's saved private
intent and requires its original caller/token. These commands never execute an
installation, replay the original job or authorize native continuation.`,
			RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
				return runNVKEAgentInstallPlanCharts(cmd, ctx, c, args[0], action)
			})}
		nvkeAgentJobFlags(leaf)
		leaf.Flags().String("plan", "", "Exact prepared plan UUID (required)")
		leaf.Flags().Int("plan-version", 0, "Exact reviewed plan version (required)")
		leaf.Flags().String("plan-digest", "", "Exact reviewed plan SHA-256 (required)")
		leaf.Flags().String("artifact", "", "Exact new or retained chart-artifact UUID (required)")
		for _, flag := range []string{"plan", "plan-version", "plan-digest", "artifact"} {
			_ = leaf.MarkFlagRequired(flag)
		}
		if verb == "prepare" {
			leaf.Flags().String("file", "", "Private complete chart JSON request or - for stdin (required)")
			leaf.Flags().String("request-file", "", "New or matching private metadata recovery journal (required)")
			_ = leaf.MarkFlagRequired("file")
			_ = leaf.MarkFlagRequired("request-file")
		}
		group.AddCommand(leaf)
	}
	return group
}

type nvkeChartLimits struct{ bytes, expanded int64 }

type nvkeChartJournal struct {
	nvkePlanJournal
	PlanVersion int    `json:"planVersion"`
	PlanDigest  string `json:"planDigest"`
	ArtifactID  string `json:"artifactId"`
}

func decodeNVKEChartStrict(data []byte, target any) error {
	allowed := map[string]bool{
		"format": true, "artifactId": true, "planId": true, "planDigest": true,
		"maximumBytes": true, "maximumExpandedBytes": true, "timeoutSeconds": true, "allowPlainHttp": true,
		"kind": true, "server": true, "organizationId": true, "actorId": true, "siteId": true,
		"siteVersion": true, "authorityVersion": true, "operationId": true, "operationVersion": true,
		"inputSha256": true, "planVersion": true,
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return errors.New("chart metadata must be one JSON object")
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, e := decoder.Token()
		key, ok := token.(string)
		if e != nil || !ok || !allowed[key] || seen[key] {
			return errors.New("duplicate or invalid chart metadata fields")
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return errors.New("invalid chart metadata value")
		}
	}
	if !json.Valid(data) {
		return errors.New("chart metadata must be one JSON object")
	}
	return decodeNVKEStrict(data, target)
}

func nvkeInstallPlanChartsIntent(cmd *cobra.Command, c *api.Client, review api.NVKEAgentJobReview, source *api.NVKEOperation, plan *api.NVKEInstallPlan, artifactID string) (string, nvkeChartLimits, error) {
	var limits nvkeChartLimits
	file, journal := resourceStringFlag(cmd, "file"), resourceStringFlag(cmd, "request-file")
	if file == "" || journal == "" || journal == "-" {
		return "", limits, errors.New("use a private chart input and a private recovery journal path")
	}
	text, err := readNVKEText(cmd, file)
	if err != nil {
		return "", limits, err
	}
	var body struct {
		Format               int             `json:"format"`
		ArtifactID           string          `json:"artifactId"`
		PlanID               string          `json:"planId"`
		PlanDigest           string          `json:"planDigest"`
		MaximumBytes         string          `json:"maximumBytes"`
		MaximumExpandedBytes string          `json:"maximumExpandedBytes"`
		TimeoutSeconds       json.RawMessage `json:"timeoutSeconds,omitempty"`
		AllowPlainHTTP       json.RawMessage `json:"allowPlainHttp,omitempty"`
	}
	if len(text) > 1024*1024 || decodeNVKEChartStrict(text, &body) != nil || body.Format != 1 || body.ArtifactID != artifactID || body.PlanID != plan.ID || body.PlanDigest != plan.Plan.PlanDigest {
		return "", limits, errors.New("review one bounded chart body matching the exact artifact, plan and digest")
	}
	limits.bytes, err = agentDecimal(body.MaximumBytes)
	if err != nil || limits.bytes == 0 {
		return "", limits, errors.New("use a positive decimal int64 chart byte budget")
	}
	limits.expanded, err = agentDecimal(body.MaximumExpandedBytes)
	if err != nil || limits.expanded == 0 {
		return "", limits, errors.New("use a positive decimal int64 chart expansion budget")
	}
	if len(body.TimeoutSeconds) > 0 {
		var deadline int
		if json.Unmarshal(body.TimeoutSeconds, &deadline) != nil || deadline < 1 || deadline > 3600 {
			return "", limits, errors.New("use an integer chart tool deadline from 1 to 3600 seconds")
		}
	}
	if len(body.AllowPlainHTTP) > 0 && !bytes.Equal(bytes.TrimSpace(body.AllowPlainHTTP), []byte("true")) && !bytes.Equal(bytes.TrimSpace(body.AllowPlainHTTP), []byte("false")) {
		return "", limits, errors.New("allowPlainHttp must be an explicit boolean when present")
	}
	saved := nvkeChartJournal{nvkePlanJournal: nvkePlanJournal{
		Format: 1, Kind: "nvke-install-plan-charts", Server: c.BaseURL(), OrganizationID: c.Org(),
		ActorID: review.ExpectedActorID, SiteID: source.SiteID, SiteVersion: review.ExpectedSiteVersion,
		AuthorityVersion: review.ExpectedAuthorityVersion, OperationID: source.ID,
		OperationVersion: review.ExpectedOperationVersion, PlanID: plan.ID, InputSHA256: nvkeParameterDigest(text),
	}, PlanVersion: plan.Version, PlanDigest: plan.Plan.PlanDigest, ArtifactID: artifactID}
	_, err = privatefile.ReadFile(journal, 16384)
	if errors.Is(err, os.ErrNotExist) {
		data, e := json.MarshalIndent(saved, "", "  ")
		if e != nil {
			return "", limits, errors.New("chart recovery metadata unavailable")
		}
		if e = writeNVKEExclusive(journal, append(data, '\n')); e != nil {
			return "", limits, e
		}
	} else if err != nil {
		return "", limits, errors.New("preserve the original private chart recovery journal")
	}
	data, err := privatefile.ReadFile(journal, 16384)
	var retained nvkeChartJournal
	if err != nil || decodeNVKEChartStrict(data, &retained) != nil || !reflect.DeepEqual(saved, retained) {
		return "", limits, errors.New("chart recovery source or exact private input changed; preserve the original artifact and journal")
	}
	return string(text), limits, nil
}

func validateNVKEInstallPlanCharts(row *api.NVKEInstallPlanCharts, plan *api.NVKEInstallPlan, artifactID string, limits nvkeChartLimits) error {
	invalid := errors.New("chart artifacts did not confirm the exact reviewed plan/source; preserve the artifact identity and refresh")
	if row == nil || row.ID != artifactID || row.Version < 1 || row.Status != "prepared" || row.PlanID != plan.ID || row.PlanVersion != plan.Version || row.PlanDigest != plan.Plan.PlanDigest || row.OperationID != plan.OperationID || row.OperationVersion != plan.OperationVersion || row.SiteID != plan.SiteID || row.SiteVersion != plan.SiteVersion || row.AuthorityID != plan.AuthorityID || row.AuthorityVersion != plan.AuthorityVersion || !nvkeDigestPattern.MatchString(row.ArtifactDigest) || row.ChartCount == nil || *row.ChartCount < 0 || *row.ChartCount > plan.ItemCount || row.ContinuationAuthorized == nil || *row.ContinuationAuthorized || row.ExecutionState != "requires-rendering-images-and-live-verification" {
		return invalid
	}
	size, e1 := agentDecimal(row.Bytes)
	expanded, e2 := agentDecimal(row.ExpandedBytes)
	if e1 != nil || e2 != nil || ((*row.ChartCount == 0) != (size == 0)) || ((*row.ChartCount == 0) != (expanded == 0)) || (limits.bytes > 0 && size > limits.bytes) || (limits.expanded > 0 && expanded > limits.expanded) {
		return invalid
	}
	return nil
}

func runNVKEAgentInstallPlanCharts(cmd *cobra.Command, ctx context.Context, c *api.Client, id, verb string) error {
	planID, artifactID, digest := resourceStringFlag(cmd, "plan"), resourceStringFlag(cmd, "artifact"), resourceStringFlag(cmd, "plan-digest")
	version, _ := cmd.Flags().GetInt("plan-version")
	if nvkeGUID(planID) != nil || nvkeGUID(artifactID) != nil || planID == id || artifactID == id || artifactID == planID || version < 1 || !nvkeDigestPattern.MatchString(digest) {
		return errors.New("review distinct exact plan/artifact UUIDs and the prepared plan version and digest")
	}
	review, source, err := nvkeAgentJobReview(cmd, ctx, c, id)
	if err != nil {
		return err
	}
	if source.Status != "UNCERTAIN" || source.OperationKey != "tooling.management.install" {
		return errors.New("chart preparation requires the original uncertain management installation")
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
		return errors.New("the prepared plan version or digest changed; review before chart preparation")
	}
	input := api.NVKEInstallPlanChartsSource{NVKEInstallPlanReview: api.NVKEInstallPlanReview{NVKEAgentJobReview: review, PlanID: planID}, ExpectedPlanVersion: version, PlanDigest: digest}
	var row *api.NVKEInstallPlanCharts
	var limits nvkeChartLimits
	if verb == "prepare" {
		var text string
		text, limits, err = nvkeInstallPlanChartsIntent(cmd, c, review, source, plan, artifactID)
		if err != nil {
			return err
		}
		row, err = c.PrepareNVKEInstallPlanCharts(ctx, api.NVKEInstallPlanChartsPrepare{NVKEInstallPlanChartsSource: input, RequestText: text})
	} else {
		row, err = c.ReviewNVKEInstallPlanCharts(ctx, api.NVKEInstallPlanChartsReview{NVKEInstallPlanChartsSource: input, ArtifactID: artifactID}, verb == "retry-preparation")
	}
	if err != nil {
		return fmt.Errorf("%w; retain --artifact %s and the exact plan/source versions; use install-plan charts review before another preparation", err, artifactID)
	}
	if err = validateNVKEInstallPlanCharts(row, plan, artifactID, limits); err != nil {
		return err
	}
	return nvkeJSON(cmd, row)
}
