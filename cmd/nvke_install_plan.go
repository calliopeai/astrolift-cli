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
	"unicode/utf8"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/nvkecatalog"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/spf13/cobra"
)

var nvkePlanItemName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func newNVKEAgentInstallPlanCommand() *cobra.Command {
	group := &cobra.Command{Use: "install-plan", Short: "Prepare and review remaining-install proposals without executing them"}
	descriptions := map[string]string{
		"prepare":           "Prepare a reviewed remaining-install proposal",
		"review":            "Read a saved proposal and recover an unconfirmed result",
		"retry-preparation": "Repeat the original saved preparation intent",
		"items":             "Page the saved deployment items",
		"workloads":         "Page the saved workload observations",
	}
	for _, verb := range []string{"prepare", "review", "retry-preparation", "items", "workloads"} {
		action := verb
		leaf := &cobra.Command{Use: verb + " OPERATION_GUID", Args: cobra.ExactArgs(1), Short: descriptions[verb], Long: `Plan preparation and review require the original uncertain installation and current
reviewed site/authority/operation versions. Preparation records a private recovery
journal before API I/O. A lost reply requires review of the same --plan identity.
retry-preparation explicitly repeats only the server's retained private intent;
it requires the original preparation caller/token. No command executes a proposal,
replays the original installation or releases its uncertainty fence.`, RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
			return runNVKEAgentInstallPlan(cmd, ctx, c, args[0], action)
		})}
		nvkeAgentJobFlags(leaf)
		leaf.Flags().String("plan", "", "Exact new or retained plan UUID (required)")
		_ = leaf.MarkFlagRequired("plan")
		if verb == "prepare" {
			leaf.Flags().String("file", "", "Private complete plan JSON body or - for stdin (required)")
			_ = leaf.MarkFlagRequired("file")
			leaf.Flags().String("request-file", "", "New or matching private metadata recovery journal (required)")
			_ = leaf.MarkFlagRequired("request-file")
		}
		if verb == "items" || verb == "workloads" {
			leaf.Flags().String("plan-digest", "", "Exact reviewed plan SHA-256 (required)")
			_ = leaf.MarkFlagRequired("plan-digest")
			leaf.Flags().Int("limit", 25, "Maximum rows in this page (1–100)")
			leaf.Flags().String("after", "", "Exact cursor from the same source/view/search/limit")
			leaf.Flags().String("search", "", "Server-side literal search (maximum 200 characters)")
		}
		group.AddCommand(leaf)
	}
	group.AddCommand(newNVKEAgentInstallPlanChartsCommand(), newNVKEAgentInstallPlanTreeCommand(), newNVKEAgentInstallPlanContextCommand(), newNVKEAgentInstallPlanVerificationCommand(), newNVKEAgentInstallPlanImageCommand())
	return group
}

func validateNVKEInstallPlan(row *api.NVKEInstallPlan, source *api.NVKEOperation, review api.NVKEAgentJobReview, selected *api.NVKEAgentAuthority, planID string) error {
	invalid := errors.New("install plan did not confirm the reviewed original source; retain its identity and refresh")
	if row == nil || row.ID != planID || row.Version < 1 || row.Status != "prepared" || row.OperationID != source.ID || row.OperationVersion != review.ExpectedOperationVersion || row.SiteID != source.SiteID || row.SiteVersion != review.ExpectedSiteVersion || row.AuthorityID != selected.ID || row.AuthorityVersion != review.ExpectedAuthorityVersion || row.ItemCount < 1 || row.ItemCount > 10000 {
		return invalid
	}
	if _, e := agentDecimal(row.RetainedBytes); e != nil {
		return invalid
	}
	if _, e := agentDecimal(row.ProposedBytes); e != nil {
		return invalid
	}
	h, s, i := row.Plan, row.Plan.Source, row.Infrastructure
	if h.Format != 1 || h.ID != planID || h.Scope != "remaining-management-install-proposal" || h.PhaseEvidence != "retained-native-stderr-order-only" || h.RequiresOriginalSourceVerification == nil || !*h.RequiresOriginalSourceVerification || h.RequiresExplicitAcknowledgement == nil || !*h.RequiresExplicitAcknowledgement || h.ContinuationAuthorized == nil || *h.ContinuationAuthorized || h.FirstRemainingOrdinal == nil || *h.FirstRemainingOrdinal < 0 || *h.FirstRemainingOrdinal >= row.ItemCount || s.SiteID != source.SiteID || s.JobID != source.ID || s.InstanceID != selected.InstanceID || s.BundleManifestSHA256 != selected.BundleManifestSHA256 || s.NativeRevision != nvkecatalog.Pinned().Revision || nvkeGUID(s.NativeRunID) != nil || s.NativeExitCode == 0 || int64(s.NativeExitCode) < -2147483648 || int64(s.NativeExitCode) > 2147483647 {
		return invalid
	}
	if s.OriginalDeploymentEvidence != "retained-prepared-deployment" && s.OriginalDeploymentEvidence != "reconstructed-prepared-deployment" {
		return invalid
	}
	for _, d := range []string{h.PlanDigest, s.BundleManifestSHA256, s.NativeArgumentsSHA256, s.RetainedInputsSHA256, s.ProposedInputsSHA256, s.ProposedExecutionSHA256, s.PrivateSourceSHA256, s.ReviewDigest, s.StderrSHA256} {
		if !nvkeDigestPattern.MatchString(d) {
			return invalid
		}
	}
	started, e1 := time.Parse(time.RFC3339Nano, s.OriginalStartedAt)
	observed, e2 := time.Parse(time.RFC3339Nano, s.ReviewObservedAt)
	prepared, e3 := time.Parse(time.RFC3339Nano, h.PreparedAt)
	infra, e4 := time.Parse(time.RFC3339Nano, i.ObservedAt)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || observed.Before(started) || prepared.Before(observed) || !infra.Equal(observed) || i.APIReady == nil || !*i.APIReady || nvkeGUID(i.ClusterUID) != nil || i.WorkloadCount == nil || *i.WorkloadCount < 0 || *i.WorkloadCount > 10000 {
		return invalid
	}
	return nil
}

type nvkePlanJournal struct {
	Format           int    `json:"format"`
	Kind             string `json:"kind"`
	Server           string `json:"server"`
	OrganizationID   string `json:"organizationId"`
	ActorID          int    `json:"actorId"`
	SiteID           string `json:"siteId"`
	SiteVersion      int    `json:"siteVersion"`
	AuthorityVersion int    `json:"authorityVersion"`
	OperationID      string `json:"operationId"`
	OperationVersion int    `json:"operationVersion"`
	PlanID           string `json:"planId"`
	InputSHA256      string `json:"inputSha256"`
}

func nvkeInstallPlanIntent(cmd *cobra.Command, c *api.Client, review api.NVKEAgentJobReview, source *api.NVKEOperation, planID string) (string, error) {
	file, journal := resourceStringFlag(cmd, "file"), resourceStringFlag(cmd, "request-file")
	if file == "" || journal == "" || journal == "-" {
		return "", errors.New("use a private plan input and a private recovery journal path")
	}
	text, err := readNVKEText(cmd, file)
	if err != nil {
		return "", err
	}
	var envelope struct {
		Format            int             `json:"format"`
		PlanID            string          `json:"planId"`
		Request           json.RawMessage `json:"request"`
		MaximumInputBytes string          `json:"maximumInputBytes"`
		Overrides         json.RawMessage `json:"overrides,omitempty"`
	}
	if len(text) > 1024*1024 || !json.Valid(text) || decodeNVKEStrict(text, &envelope) != nil || envelope.Format != 1 || envelope.PlanID != planID || len(envelope.Request) == 0 {
		return "", errors.New("review one bounded plan body matching --plan, with format, request and maximumInputBytes")
	}
	budget, e := agentDecimal(envelope.MaximumInputBytes)
	if e != nil || budget == 0 {
		return "", errors.New("use a positive decimal int64 plan input byte budget")
	}
	if !bytes.Equal(bytes.TrimSpace(envelope.Request), []byte("null")) && bytes.TrimSpace(envelope.Request)[0] != '{' {
		return "", errors.New("the proposed request must be null or a full native install envelope")
	}
	saved := nvkePlanJournal{1, "nvke-install-plan", c.BaseURL(), c.Org(), review.ExpectedActorID, source.SiteID, review.ExpectedSiteVersion, review.ExpectedAuthorityVersion, source.ID, review.ExpectedOperationVersion, planID, nvkeParameterDigest(text)}
	_, err = privatefile.ReadFile(journal, 16384)
	if errors.Is(err, os.ErrNotExist) {
		data, encodeErr := json.MarshalIndent(saved, "", "  ")
		if encodeErr != nil {
			return "", errors.New("plan recovery metadata unavailable")
		}
		if err = writeNVKEExclusive(journal, append(data, '\n')); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", errors.New("preserve the original private plan recovery journal")
	}
	data, err := privatefile.ReadFile(journal, 16384)
	var persisted nvkePlanJournal
	if err != nil || !json.Valid(data) || decodeNVKEStrict(data, &persisted) != nil || !reflect.DeepEqual(saved, persisted) {
		return "", errors.New("plan recovery source or private input changed; preserve the original plan and journal")
	}
	return string(text), nil
}

func runNVKEAgentInstallPlan(cmd *cobra.Command, ctx context.Context, c *api.Client, id, verb string) error {
	planID := resourceStringFlag(cmd, "plan")
	if nvkeGUID(planID) != nil || planID == id {
		return errors.New("use an exact plan UUID distinct from the original operation")
	}
	if verb == "items" || verb == "workloads" {
		limit, _ := cmd.Flags().GetInt("limit")
		search, cursor, digest := resourceStringFlag(cmd, "search"), resourceStringFlag(cmd, "after"), resourceStringFlag(cmd, "plan-digest")
		if limit < 1 || limit > 100 || !utf8.ValidString(search) || len([]rune(search)) > 200 || !nvkeDigestPattern.MatchString(digest) || (cmd.Flags().Changed("after") && cursor == "") || len(cursor) > 16384 {
			return errors.New("use a bounded plan page, search, digest and exact cursor")
		}
	}
	review, source, err := nvkeAgentJobReview(cmd, ctx, c, id)
	if err != nil {
		return err
	}
	if source.Status != "UNCERTAIN" || source.OperationKey != "tooling.management.install" {
		return errors.New("plan review requires the original uncertain management installation")
	}
	selected, err := c.NVKEAgentAuthority(ctx, source.SiteID)
	if err != nil {
		return err
	}
	if err = validateNVKEAgentReply(selected, api.NVKEAgentReview{SiteID: source.SiteID, ExpectedSiteVersion: review.ExpectedSiteVersion, ExpectedVersion: review.ExpectedAuthorityVersion}, "verify"); err != nil {
		return err
	}
	input := api.NVKEInstallPlanReview{NVKEAgentJobReview: review, PlanID: planID}
	var row *api.NVKEInstallPlan
	if verb == "prepare" {
		text, e := nvkeInstallPlanIntent(cmd, c, review, source, planID)
		if e != nil {
			return e
		}
		row, err = c.PrepareNVKEInstallPlan(ctx, api.NVKEInstallPlanPrepare{NVKEAgentJobReview: review, RequestText: text})
	} else {
		row, err = c.ReviewNVKEInstallPlan(ctx, input, verb == "retry-preparation")
	}
	if err != nil {
		return fmt.Errorf("%w; retain --plan %s and reviewed versions; use install-plan review before another preparation", err, planID)
	}
	if err = validateNVKEInstallPlan(row, source, review, selected, planID); err != nil {
		return err
	}
	if verb != "items" && verb != "workloads" {
		return nvkeJSON(cmd, row)
	}
	limit, _ := cmd.Flags().GetInt("limit")
	search := resourceStringFlag(cmd, "search")
	digest := resourceStringFlag(cmd, "plan-digest")
	cursor := resourceStringFlag(cmd, "after")
	if limit < 1 || limit > 100 || len([]rune(search)) > 200 || !nvkeDigestPattern.MatchString(digest) || digest != row.Plan.PlanDigest || (cmd.Flags().Changed("after") && cursor == "") || len(cursor) > 16384 {
		return errors.New("use the reviewed plan digest and a bounded page/search with its exact cursor")
	}
	var after *string
	if cursor != "" {
		after = &cursor
	}
	if verb == "items" {
		page, e := c.NVKEInstallPlanItems(ctx, input, digest, after, limit, search)
		if e != nil {
			return e
		}
		if e = validateNVKEPlanItems(page, row, limit, search, after == nil); e != nil {
			return e
		}
		return nvkeJSON(cmd, page)
	}
	page, e := c.NVKEInstallPlanWorkloads(ctx, input, digest, after, limit, search)
	if e != nil {
		return e
	}
	if e = validateNVKEPlanPage(page, row, "workloads", *row.Infrastructure.WorkloadCount, limit, search, after == nil); e != nil {
		return e
	}
	if e = validateNVKEInstallWorkloads(page.Items, errors.New("invalid plan workload observations")); e != nil {
		return e
	}
	return nvkeJSON(cmd, page)
}
func validateNVKEPlanPage[T any](page *api.NVKEInstallPlanPage[T], plan *api.NVKEInstallPlan, view string, maximum, limit int, search string, initial bool) error {
	invalid := errors.New("install plan page does not match its reviewed snapshot")
	if page == nil || page.PlanID != plan.ID || page.PlanDigest != plan.Plan.PlanDigest || page.View != view || page.Items == nil || page.TotalCount == nil || *page.TotalCount < 0 || *page.TotalCount > maximum || (search == "" && *page.TotalCount != maximum) || len(page.Items) > limit || len(page.Items) > *page.TotalCount {
		return invalid
	}
	total := *page.TotalCount
	if total > 0 && len(page.Items) == 0 {
		return invalid
	}
	if page.NextCursor != nil && (*page.NextCursor == "" || len(*page.NextCursor) > 16384 || len(page.Items) != limit || total <= limit) {
		return invalid
	}
	if initial && (len(page.Items) != min(limit, total) || (page.NextCursor != nil) != (total > limit)) {
		return invalid
	}
	return nil
}
func validateNVKEPlanItems(page *api.NVKEInstallPlanPage[api.NVKEInstallPlanItem], plan *api.NVKEInstallPlan, limit int, search string, initial bool) error {
	if err := validateNVKEPlanPage(page, plan, "items", plan.ItemCount, limit, search, initial); err != nil {
		return err
	}
	invalid := errors.New("install plan item identity, phase observation or proposed action is invalid")
	previous := -1
	seen := map[string]bool{}
	for _, i := range page.Items {
		if i.Ordinal == nil || *i.Ordinal <= previous || *i.Ordinal >= plan.ItemCount || !nvkePlanItemName.MatchString(i.Name) || seen[i.Name] || !nvkeDigestPattern.MatchString(i.ContentSHA256) || !nvkeDigestPattern.MatchString(i.OriginalContentSHA256) {
			return invalid
		}
		for _, kind := range []string{i.Type, i.OriginalType} {
			if kind != "manifest" && kind != "template" && kind != "helm" {
				return invalid
			}
		}
		if search == "" && ((previous < 0 && initial && *i.Ordinal != 0) || (previous >= 0 && *i.Ordinal != previous+1)) {
			return invalid
		}
		ordinal, first := *i.Ordinal, *plan.Plan.FirstRemainingOrdinal
		observation, action := "not-observed", "execute"
		if ordinal < first {
			observation, action = "returned-before-next-observed-item", "skip"
		} else if ordinal == first {
			observation = "last-observed-in-unsuccessful-run"
		}
		if i.OriginalObservation != observation || i.ProposedAction != action {
			return invalid
		}
		seen[i.Name] = true
		previous = ordinal
	}
	return nil
}
