package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"unicode"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/nvkecatalog"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const nvkeNativeResetKind = "nvke-native-rotation-reset"
const nvkeNativeResetKey = "temporal.reset.RotateEtcdEncryptionKey"
const nvkeNativeResetFormat = "nvke-native-rotation-reset-review-v1"

func nvkeNativeSourceFlags(cmd *cobra.Command) {
	nvkeReadSiteFlags(cmd)
	cmd.Flags().Int("site-version", 0, "Reviewed site version")
	cmd.Flags().Int("authority-version", 0, "Reviewed native Temporal authority version")
	cmd.Flags().String("source-operation", "", "Reviewed successful controller get-cluster operation GUID")
	cmd.Flags().Int("source-version", 0, "Reviewed source operation version")
}

func nativeNVKEEventID(value string) bool {
	number, err := strconv.ParseInt(value, 10, 64)
	return err == nil && number > 0 && strconv.FormatInt(number, 10) == value
}

func newNVKENativeHistoryCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "history WORKFLOW", Args: cobra.ExactArgs(1), Short: "Inspect one exact native owner history page without private payloads", Long: `Inspect RenewK8sCertsV2 or RotateEtcdEncryptionKey using the current successful
get-cluster observation and explicit site/authority/source versions. The server
derives the recorded owner; arbitrary native workflow IDs cannot be substituted.
Event IDs are decimal strings. Return nextCursor as --after with the same review
and page size. A changed native run invalidates the cursor. This is read-only.`, RunE: nvkeScoped(runNVKENativeHistory)}
	nvkeNativeSourceFlags(cmd)
	nvkePageFlags(cmd)
	return cmd
}

func runNVKENativeHistory(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
	site, source := resourceStringFlag(cmd, "site"), resourceStringFlag(cmd, "source-operation")
	siteVersion, _ := cmd.Flags().GetInt("site-version")
	authorityVersion, _ := cmd.Flags().GetInt("authority-version")
	sourceVersion, _ := cmd.Flags().GetInt("source-version")
	after, limit, err := nvkePage(cmd)
	if err != nil {
		return err
	}
	if nvkeGUID(site) != nil || nvkeGUID(source) != nil || siteVersion < 1 || authorityVersion < 1 || sourceVersion < 1 || (args[0] != "RenewK8sCertsV2" && args[0] != "RotateEtcdEncryptionKey") {
		return errors.New("use a renewal/rotation workflow, exact site/source GUIDs and positive reviewed versions")
	}
	_, actor, err := reviewedRequestScope(cmd, c)
	if err != nil {
		return errors.New("native history caller/server/organization identity unavailable")
	}
	input := api.NVKENativeHistoryInput{SiteID: site, ExpectedSiteVersion: siteVersion, ExpectedActorID: actor, ExpectedAuthorityVersion: authorityVersion, SourceOperationID: source, ExpectedSourceVersion: sourceVersion, Workflow: args[0], Limit: limit}
	if after != "" {
		input.After = &after
	}
	page, err := c.NVKENativeHistory(ctx, input)
	if err != nil {
		return err
	}
	if page == nil || page.SiteID != site || page.SiteVersion != siteVersion || page.AuthorityVersion != authorityVersion || page.SourceOperationID != source || page.SourceVersion != sourceVersion || page.Workflow != args[0] || nvkeGUID(page.ClusterID) != nil || nvkeGUID(page.RunID) != nil || page.WorkflowID == "" || page.ObservedAt == "" || page.Items == nil || len(page.Items) > limit {
		return errors.New("native history reply did not match the exact reviewed identity")
	}
	var previous int64
	for _, event := range page.Items {
		if !nativeNVKEEventID(event.EventID) {
			return errors.New("native history returned an inexact event ID")
		}
		id, _ := strconv.ParseInt(event.EventID, 10, 64)
		if id <= previous || event.EventType == "" {
			return errors.New("native history returned an invalid event order")
		}
		previous = id
		for _, link := range []*string{event.ScheduledEventID, event.WorkflowTaskCompletedEventID} {
			if link != nil && !nativeNVKEEventID(*link) {
				return errors.New("native history returned an inexact scheduling reference")
			}
		}
	}
	return nvkeJSON(cmd, page)
}

func newNVKENativeResetCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "reset", Args: cobra.NoArgs, Short: "Submit a reviewed etcd rotation reset with durable recovery", Long: `Reset only RotateEtcdEncryptionKey at a reviewed phase-specific native boundary.
Use a private JSON --file containing exact workflow/run/event IDs, activity type,
phase/mode, rationale, node-work evidence and both explicit acknowledgements.
Astro checks native ownership/history; operator evidence is an attestation, not
an independent proof that agents stopped. Coordinate external Temporal operators.
The server sends at most one native reset RPC. An unconfirmed sending intent is
never resent, including a crash before RPC. Preserve the private recovery file.
Renewal reset is unsupported. No native lock is released by this command.`, RunE: nvkeScoped(runNVKENativeReset)}
	nvkeNativeSourceFlags(cmd)
	cmd.Flags().String("request-file", "", "New or original private reset recovery path (required)")
	_ = cmd.MarkFlagRequired("request-file")
	cmd.Flags().String("file", "", "Private reset review JSON, or - for stdin; required without a visible receipt")
	cmd.Flags().Int("operation-timeout", 7200, "Astro observation deadline seconds (30–86400); does not cancel native work")
	cmd.Flags().Bool("wait", false, "Wait on Astro observation; native workflow completion is not encryption health")
	nvkeWaitFlags(cmd)
	recover := &cobra.Command{Use: "recover", Args: cobra.NoArgs, Short: "Look up an existing reset receipt without reading input or re-admitting", RunE: nvkeScoped(runNVKENativeResetRecover)}
	recover.Flags().String("request-file", "", "Original private reset recovery file (required)")
	_ = recover.MarkFlagRequired("request-file")
	recover.Flags().Bool("wait", false, "Wait on an existing Astro receipt only")
	nvkeWaitFlags(recover)
	cmd.AddCommand(recover)
	return cmd
}

func decodeNVKENativeReset(data []byte) (api.NVKENativeResetReview, error) {
	var review api.NVKENativeResetReview
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return review, errors.New("reset review must be one JSON object")
	}
	seen := map[string]bool{}
	allowed := map[string]bool{}
	shape := reflect.TypeOf(review)
	for i := 0; i < shape.NumField(); i++ {
		allowed[shape.Field(i).Tag.Get("json")] = true
	}
	for decoder.More() {
		key, keyErr := decoder.Token()
		name, ok := key.(string)
		if keyErr != nil || !ok || seen[name] || !allowed[name] {
			return review, errors.New("reset review contains invalid or duplicate fields")
		}
		seen[name] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return review, errors.New("reset review contains invalid fields")
		}
	}
	if _, err = decoder.Token(); err != nil {
		return review, errors.New("reset review contains invalid fields")
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&review) != nil {
		return review, errors.New("reset review fields do not match the declared format")
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return review, errors.New("reset review contains trailing data")
	}
	if nvkeGUID(review.BaseRunID) != nil || !nativeNVKEEventID(review.WorkflowTaskEventID) || !nativeNVKEEventID(review.ActivityEventID) || !review.AcknowledgeBoundaryReviewed || !review.AcknowledgeConflictingWorkStopped {
		return review, errors.New("reset review needs an exact base run, decimal-string events and both explicit acknowledgements")
	}
	if !nativeResetText(review.WorkflowID, 1000, false) || !nativeResetText(review.ActivityType, 128, false) || !nativeResetText(review.Rationale, 4000, true) || !nativeResetText(review.QuiescenceEvidence, 4000, true) || (review.RetirementWorkflowID != "" && !nativeResetText(review.RetirementWorkflowID, 1000, false)) {
		return review, errors.New("reset review identifiers, rationale or evidence are empty, oversized or contain invalid controls")
	}
	switch review.Phase {
	case "prepare", "add_secondary", "promote_primary", "rewrite", "remove_old_keys", "finalize":
	default:
		return review, errors.New("choose a documented reset phase")
	}
	switch review.Mode {
	case "retry_activity", "restart_rewrite", "resume_rewrite":
	case "refresh_survivors":
		if review.RetirementWorkflowID == "" {
			return review, errors.New("survivor refresh requires the completed retirement workflow")
		}
	default:
		return review, errors.New("choose a documented reset mode")
	}

	return review, nil
}

func nativeResetText(value string, maximum int, multiline bool) bool {
	if strings.TrimSpace(value) == "" || len(value) > maximum {
		return false
	}
	for _, r := range value {
		if unicode.IsPrint(r) || (multiline && strings.ContainsRune("\n\r\t", r)) {
			continue
		}
		return false
	}
	return true
}

func runNVKENativeResetRecover(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
	if boolFlag(cmd, "wait") {
		if err := validateNVKEWait(cmd); err != nil {
			return err
		}
	}
	saved, err := readNVKENativeRecoveryKind(resourceStringFlag(cmd, "request-file"), nvkeNativeResetKind)
	if err != nil {
		return errors.New("reset recovery file unavailable; preserve its original private contents")
	}
	server, actor, err := reviewedRequestScope(cmd, c)
	if err != nil {
		return errors.New("native reset caller/server/organization identity unavailable")
	}
	if err = saved.checkScope(nvkeNativeResetKind, server, c.Org(), actor); err != nil {
		return err
	}
	row, err := c.NVKENativeRequestReceipt(ctx, saved.TargetID, saved.RequestID)
	if err != nil {
		return err
	}
	if row == nil {
		return errors.New("no reset receipt is visible; preserve the original review and recovery file before inspecting the same request")
	}
	if err = validateNVKENativeReceiptKey(saved, row, nvkeNativeResetKey); err != nil {
		return err
	}
	return finishNVKERequest(cmd, ctx, c, row)
}

func runNVKENativeReset(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
	site, filename := resourceStringFlag(cmd, "site"), resourceStringFlag(cmd, "request-file")
	if nvkeGUID(site) != nil || filename == "" || filename == "-" {
		return errors.New("use an exact site GUID and private reset recovery path")
	}
	if boolFlag(cmd, "wait") {
		if err := validateNVKEWait(cmd); err != nil {
			return err
		}
	}
	server, actor, err := reviewedRequestScope(cmd, c)
	if err != nil {
		return errors.New("native reset caller/server/organization identity unavailable")
	}
	saved, err := readNVKENativeRecoveryKind(filename, nvkeNativeResetKind)
	if err == nil {
		if err = saved.checkScope(nvkeNativeResetKind, server, c.Org(), actor); err != nil {
			return err
		}
		if saved.TargetID != site {
			return errors.New("reset recovery belongs to another site")
		}
		row, lookupErr := c.NVKENativeRequestReceipt(ctx, site, saved.RequestID)
		if lookupErr != nil {
			return lookupErr
		}
		if row != nil {
			if err = validateNVKENativeReceiptKey(saved, row, nvkeNativeResetKey); err != nil {
				return err
			}
			return finishNVKERequest(cmd, ctx, c, row)
		}
		for flag, expected := range map[string]int{"site-version": saved.Version, "authority-version": saved.AuthorityVersion, "source-version": saved.SourceVersion, "operation-timeout": saved.IntervalSeconds} {
			actual, _ := cmd.Flags().GetInt(flag)
			if cmd.Flags().Changed(flag) && actual != expected {
				return errors.New("pending reset cannot change reviewed versions or observation deadline")
			}
		}
		if cmd.Flags().Changed("source-operation") && resourceStringFlag(cmd, "source-operation") != saved.SourceOperationID {
			return errors.New("pending reset cannot change the reviewed source")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("reset recovery file unavailable; preserve its original private contents")
	}
	inputFile := resourceStringFlag(cmd, "file")
	if inputFile == "" {
		return errors.New("--file is required when the original reset receipt is not visible")
	}
	parameters, err := readNVKEText(cmd, inputFile)
	if err != nil {
		return err
	}
	if len(parameters) > 65536 {
		return errors.New("reset review exceeds 64 KiB")
	}
	review, err := decodeNVKENativeReset(parameters)
	if err != nil {
		return err
	}
	if saved != nil {
		if saved.ExpectedSource != nvkeParameterDigest(parameters) {
			return errors.New("pending reset requires identical original UTF-8 review text")
		}
	} else {
		siteVersion, _ := cmd.Flags().GetInt("site-version")
		authorityVersion, _ := cmd.Flags().GetInt("authority-version")
		sourceVersion, _ := cmd.Flags().GetInt("source-version")
		timeout, _ := cmd.Flags().GetInt("operation-timeout")
		sourceID := resourceStringFlag(cmd, "source-operation")
		if siteVersion < 1 || authorityVersion < 1 || sourceVersion < 1 || nvkeGUID(sourceID) != nil || timeout < 30 || timeout > 86400 {
			return errors.New("new resets require reviewed site/authority/source versions, source GUID and deadline 30–86400")
		}
		siteRow, siteErr := c.NVKESite(ctx, site)
		if siteErr != nil {
			return siteErr
		}
		authority, authorityErr := c.NVKETemporalAuthority(ctx, site)
		if authorityErr != nil {
			return authorityErr
		}
		contract, contractErr := c.NVKEContract(ctx)
		if contractErr != nil {
			return contractErr
		}
		source, sourceErr := c.NVKEOperation(ctx, sourceID)
		if sourceErr != nil {
			return sourceErr
		}
		if authority == nil || validateNVKETemporalReply(authority, api.NVKETemporalReview{SiteID: site}, "get") != nil || !authority.IsEnabled || authority.Version != authorityVersion || authority.SiteVersion != siteVersion || siteRow.Version != siteVersion || !siteRow.IsActive || siteRow.UpstreamRevision != contract.Revision || contract.Revision != nvkecatalog.Pinned().Revision || source.SiteID != site || source.Version != sourceVersion || source.OperationKey != "getCluster" || source.Status != "SUCCEEDED" || source.IsInventory {
			return errors.New("reset authority or source changed; refresh the exact review")
		}
		saved = &nvkeNativeRequest{reviewedStartRequest: reviewedStartRequest{Format: 1, Kind: nvkeNativeResetKind, Server: server, OrganizationID: c.Org(), ActorUserID: actor, TargetID: site, TargetName: siteRow.Slug, RequestID: uuid.NewString(), Version: siteVersion, Ref: "RotateEtcdEncryptionKey", Revision: contract.Revision, InputSchemaDigest: nvkeParameterDigest([]byte(nvkeNativeResetFormat)), IntervalSeconds: timeout, ExpectedSource: nvkeParameterDigest(parameters)}, AuthorityVersion: authorityVersion, SourceOperationID: sourceID, SourceVersion: sourceVersion}
		data, marshalErr := json.MarshalIndent(saved, "", "  ")
		if marshalErr != nil {
			return errors.New("reset recovery metadata could not be encoded")
		}
		if err = writeNVKEExclusive(filename, append(data, '\n')); err != nil {
			return err
		}
	}
	persisted, err := readNVKENativeRecoveryKind(filename, nvkeNativeResetKind)
	if err != nil || !reflect.DeepEqual(saved, persisted) {
		return errors.New("reset recovery identity changed before submission")
	}
	row, err := c.RequestNVKENativeReset(ctx, api.NVKENativeResetInput{NVKENativeResetReview: review, SiteID: saved.TargetID, ExpectedSiteVersion: saved.Version, ExpectedActorID: saved.ActorUserID, ExpectedAuthorityVersion: saved.AuthorityVersion, SourceOperationID: saved.SourceOperationID, ExpectedSourceVersion: saved.SourceVersion, RequestID: saved.RequestID, Workflow: "RotateEtcdEncryptionKey", TimeoutSeconds: saved.IntervalSeconds})
	if err != nil {
		return fmt.Errorf("%w; preserve the original reset --request-file and private review", err)
	}
	if err = validateNVKENativeReceiptKey(saved, row, nvkeNativeResetKey); err != nil {
		return err
	}
	return finishNVKERequest(cmd, ctx, c, row)
}
