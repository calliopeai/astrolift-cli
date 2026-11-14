package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/nvkecatalog"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const nvkeNativeRequestKind = "nvke-native-workflow"

type nvkeNativeRequest struct {
	reviewedStartRequest
	AuthorityVersion            int    `json:"authorityVersion"`
	SourceOperationID           string `json:"sourceOperationId"`
	SourceVersion               int    `json:"sourceVersion"`
	AcknowledgeLostNodeTeardown bool   `json:"acknowledgeLostNodeTeardown"`
}

func nvkeKnownNativeWorkflow(name string) bool {
	for _, item := range nvkecatalog.Pinned().NativeWorkflows {
		if item.Name == name {
			return true
		}
	}
	return false
}

func newNVKENativeWorkflowCommand() *cobra.Command {
	group := &cobra.Command{Use: "workflow", Short: "Submit and recover reviewed native maintenance workflows", Long: `Submit native renewal, etcd key rotation or lost-node retirement through Astro's
encrypted outbox. Inputs use a private JSON file; the server owns native admission.
The recovery file is durable before submission and contains metadata/digests only.
After a lost reply, retain that file and original input. Recovery looks up the
original receipt before input is read and never starts another native identity.
Observation deadlines do not cancel native work or release retained locks.
Successful native execution is not site/hardware qualification.`}
	group.AddCommand(newNVKENativeRequestCommand("request WORKFLOW", ""))
	group.AddCommand(newNVKENativeHistoryCommand(), newNVKENativeResetCommand())
	for _, item := range nvkecatalog.Pinned().NativeWorkflows {
		leaf := newNVKENativeRequestCommand(nativeNVKEName(item.Name), item.Name)
		leaf.Aliases = []string{item.Name}
		leaf.Short = "Submit " + item.Name + " (" + item.Permission + ") with a durable recovery identity"
		group.AddCommand(leaf)
	}
	recover := &cobra.Command{Use: "recover", Args: cobra.NoArgs, Short: "Look up the original native receipt without input or another admission", RunE: nvkeScoped(runNVKENativeRecover)}
	recover.Flags().String("request-file", "", "Original private native workflow recovery file (required)")
	_ = recover.MarkFlagRequired("request-file")
	recover.Flags().Bool("wait", false, "Wait on an existing receipt; never submit a request")
	nvkeWaitFlags(recover)
	group.AddCommand(recover)
	return group
}

func newNVKENativeRequestCommand(use, fixed string) *cobra.Command {
	args := cobra.NoArgs
	if fixed == "" {
		args = cobra.ExactArgs(1)
	}
	cmd := &cobra.Command{Use: use, Args: args, Short: "Submit a reviewed native workflow request", RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
		name := fixed
		if name == "" {
			name = args[0]
		}
		return runNVKENativeRequest(cmd, ctx, c, name)
	})}
	nvkeReadSiteFlags(cmd)
	cmd.Flags().String("request-file", "", "New or original private recovery file (required)")
	_ = cmd.MarkFlagRequired("request-file")
	cmd.Flags().String("file", "", "Private exact native JSON input, or - for stdin (required on first admission)")
	cmd.Flags().Int("site-version", 0, "Reviewed site version; required on first admission")
	cmd.Flags().Int("authority-version", 0, "Reviewed native authority version; required on first admission")
	cmd.Flags().String("source-operation", "", "Reviewed successful controller get-cluster operation GUID")
	cmd.Flags().Int("source-version", 0, "Reviewed source operation version; required on first admission")
	cmd.Flags().Bool("acknowledge-lost-node-teardown", false, "Acknowledge permanent removal of the exact lost control-plane node")
	cmd.Flags().Int("operation-timeout", 7200, "Astro observation deadline seconds (30–86400); no native execution timeout")
	cmd.Flags().Bool("wait", false, "Wait on Astro observation; local timeout does not cancel native work")
	nvkeWaitFlags(cmd)
	return cmd
}

func validateNVKENativeRecovery(r *nvkeNativeRequest) error {
	return validateNVKENativeRecoveryKind(r, nvkeNativeRequestKind)
}

func validateNVKENativeRecoveryKind(r *nvkeNativeRequest, kind string) error {
	if kind != nvkeNativeRequestKind && kind != nvkeNativeResetKind {
		return errors.New("unknown native recovery format")
	}
	if r == nil || r.Format != 1 || r.Kind != kind || r.NVKESource != nil || r.ActorUserID < 1 || r.Version < 1 || r.AuthorityVersion < 1 || r.SourceVersion < 1 || nvkeGUID(r.TargetID) != nil || nvkeGUID(r.RequestID) != nil || nvkeGUID(r.SourceOperationID) != nil || !nvkeKnownNativeWorkflow(r.Ref) || r.IntervalSeconds < 30 || r.IntervalSeconds > 86400 || !nvkeDigestPattern.MatchString(r.ExpectedSource) || !nvkeDigestPattern.MatchString(r.InputSchemaDigest) || !nvkeRevisionPattern.MatchString(r.Revision) || (r.Ref == "RetireLostEtcdRotationNode" && !r.AcknowledgeLostNodeTeardown) {
		return errors.New("native workflow recovery metadata is invalid; preserve the original file")
	}
	if kind == nvkeNativeResetKind && (r.Ref != "RotateEtcdEncryptionKey" || r.AcknowledgeLostNodeTeardown || r.InputSchemaDigest != nvkeParameterDigest([]byte(nvkeNativeResetFormat))) {
		return errors.New("reset recovery metadata does not match its declared format")
	}
	return nil
}

func readNVKENativeRecovery(filename string) (*nvkeNativeRequest, error) {
	return readNVKENativeRecoveryKind(filename, nvkeNativeRequestKind)
}

func readNVKENativeRecoveryKind(filename, kind string) (*nvkeNativeRequest, error) {
	data, err := privatefile.ReadFile(filename, 16384)
	if err != nil {
		return nil, err
	}
	var r nvkeNativeRequest
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil {
		return nil, errors.New("native workflow recovery metadata could not be decoded")
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		return nil, errors.New("native workflow recovery file contains extra data")
	}
	if err = validateNVKENativeRecoveryKind(&r, kind); err != nil {
		return nil, err
	}
	return &r, nil
}

func validateNVKENativeReceipt(saved *nvkeNativeRequest, row *api.NVKEOperation) error {
	return validateNVKENativeReceiptKey(saved, row, "temporal."+saved.Ref)
}

func validateNVKENativeReceiptKey(saved *nvkeNativeRequest, row *api.NVKEOperation, key string) error {
	if row == nil || nvkeGUID(row.ID) != nil || row.Version < 1 || row.SiteID != saved.TargetID || row.RequestID != saved.RequestID || row.OperationKey != key || row.IsInventory || row.ResourceSource != nil {
		return errors.New("native workflow receipt did not confirm the original identity; preserve recovery")
	}
	return nil
}

func runNVKENativeRecover(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
	if boolFlag(cmd, "wait") {
		if err := validateNVKEWait(cmd); err != nil {
			return err
		}
	}
	saved, err := readNVKENativeRecovery(resourceStringFlag(cmd, "request-file"))
	if err != nil {
		return errors.New("native workflow recovery file unavailable; preserve its private protections and original contents")
	}
	server, actor, err := reviewedRequestScope(cmd, c)
	if err != nil {
		return errors.New("native workflow caller/server/organization identity could not be verified")
	}
	if err = saved.checkScope(nvkeNativeRequestKind, server, c.Org(), actor); err != nil {
		return err
	}
	row, err := c.NVKENativeRequestReceipt(ctx, saved.TargetID, saved.RequestID)
	if err != nil {
		return err
	}
	if row == nil {
		return errors.New("no native request receipt is visible; retain the original file and input before reviewing the same request again")
	}
	if err = validateNVKENativeReceipt(saved, row); err != nil {
		return err
	}
	return finishNVKERequest(cmd, ctx, c, row)
}

func runNVKENativeRequest(cmd *cobra.Command, ctx context.Context, c *api.Client, name string) error {
	site := resourceStringFlag(cmd, "site")
	filename := resourceStringFlag(cmd, "request-file")
	if !nvkeKnownNativeWorkflow(name) || nvkeGUID(site) != nil || filename == "" || filename == "-" {
		return errors.New("use a pinned native workflow, exact site GUID and private recovery path")
	}
	if boolFlag(cmd, "wait") {
		if err := validateNVKEWait(cmd); err != nil {
			return err
		}
	}
	server, actor, err := reviewedRequestScope(cmd, c)
	if err != nil {
		return errors.New("native workflow caller/server/organization identity could not be verified")
	}
	saved, err := readNVKENativeRecovery(filename)
	if err == nil {
		if err = saved.checkScope(nvkeNativeRequestKind, server, c.Org(), actor); err != nil {
			return err
		}
		if saved.TargetID != site || saved.Ref != name {
			return errors.New("native workflow recovery belongs to another site or workflow")
		}
		row, lookupErr := c.NVKENativeRequestReceipt(ctx, site, saved.RequestID)
		if lookupErr != nil {
			return lookupErr
		}
		if row != nil {
			if err = validateNVKENativeReceipt(saved, row); err != nil {
				return err
			}
			return finishNVKERequest(cmd, ctx, c, row)
		}
		for flag, expected := range map[string]int{"site-version": saved.Version, "authority-version": saved.AuthorityVersion, "source-version": saved.SourceVersion, "operation-timeout": saved.IntervalSeconds} {
			actual, _ := cmd.Flags().GetInt(flag)
			if cmd.Flags().Changed(flag) && actual != expected {
				return errors.New("pending native request cannot change reviewed versions or observation deadline")
			}
		}
		if (cmd.Flags().Changed("source-operation") && resourceStringFlag(cmd, "source-operation") != saved.SourceOperationID) || (cmd.Flags().Changed("acknowledge-lost-node-teardown") && boolFlag(cmd, "acknowledge-lost-node-teardown") != saved.AcknowledgeLostNodeTeardown) {
			return errors.New("pending native request cannot change its reviewed source or teardown acknowledgement")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("native workflow recovery file unavailable; preserve its original private contents")
	}
	inputFile := resourceStringFlag(cmd, "file")
	if inputFile == "" {
		return errors.New("--file is required when the original receipt is not visible")
	}
	parameters, err := readNVKEText(cmd, inputFile)
	if err != nil {
		return err
	}
	if len(parameters) > 65536 {
		return errors.New("native workflow input exceeds 64 KiB")
	}
	if saved != nil {
		if saved.ExpectedSource != nvkeParameterDigest(parameters) {
			return errors.New("pending native request requires the identical original UTF-8 input text")
		}
	} else {
		siteVersion, _ := cmd.Flags().GetInt("site-version")
		authorityVersion, _ := cmd.Flags().GetInt("authority-version")
		sourceVersion, _ := cmd.Flags().GetInt("source-version")
		timeout, _ := cmd.Flags().GetInt("operation-timeout")
		sourceID := resourceStringFlag(cmd, "source-operation")
		ack := boolFlag(cmd, "acknowledge-lost-node-teardown")
		if siteVersion < 1 || authorityVersion < 1 || sourceVersion < 1 || nvkeGUID(sourceID) != nil || timeout < 30 || timeout > 86400 || (name == "RetireLostEtcdRotationNode" && !ack) {
			return errors.New("new native requests require reviewed site/authority/source versions, a source GUID, deadline 30–86400 and explicit lost-node teardown acknowledgement when retiring")
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
		var schema json.RawMessage
		for _, item := range contract.NativeWorkflows {
			if item.Name == name && item.Permission == "nvke.admin" {
				schema = item.InputSchema
			}
		}
		var schemaShape struct {
			Type string `json:"type"`
		}
		schemaError := json.Unmarshal(schema, &schemaShape)
		if authority == nil || validateNVKETemporalReply(authority, api.NVKETemporalReview{SiteID: site}, "get") != nil || !authority.IsEnabled || authority.Version != authorityVersion || authority.SiteVersion != siteVersion || siteRow.Version != siteVersion || !siteRow.IsActive || siteRow.UpstreamRevision != contract.Revision || contract.Revision != nvkecatalog.Pinned().Revision || schemaError != nil || schemaShape.Type != "object" || source.SiteID != site || source.Version != sourceVersion || source.OperationKey != "getCluster" || source.Status != "SUCCEEDED" || source.IsInventory {
			return errors.New("native workflow, authority or cluster observation changed; refresh the review")
		}
		saved = &nvkeNativeRequest{reviewedStartRequest: reviewedStartRequest{Format: 1, Kind: nvkeNativeRequestKind, Server: server, OrganizationID: c.Org(), ActorUserID: actor, TargetID: site, TargetName: siteRow.Slug, RequestID: uuid.NewString(), Version: siteVersion, Ref: name, Revision: contract.Revision, InputSchemaDigest: nvkeParameterDigest(schema), IntervalSeconds: timeout, ExpectedSource: nvkeParameterDigest(parameters)}, AuthorityVersion: authorityVersion, SourceOperationID: sourceID, SourceVersion: sourceVersion, AcknowledgeLostNodeTeardown: ack}
		if err = validateNVKENativeRecovery(saved); err != nil {
			return err
		}
		data, marshalErr := json.MarshalIndent(saved, "", "  ")
		if marshalErr != nil {
			return errors.New("native recovery metadata could not be encoded")
		}
		if err = writeNVKEExclusive(filename, append(data, '\n')); err != nil {
			return err
		}
	}
	persisted, err := readNVKENativeRecovery(filename)
	if err != nil || !reflect.DeepEqual(saved, persisted) {
		return errors.New("native workflow recovery identity changed before submission")
	}
	row, err := c.RequestNVKENativeWorkflow(ctx, api.NVKENativeWorkflowInput{SiteID: saved.TargetID, ExpectedSiteVersion: saved.Version, ExpectedActorID: saved.ActorUserID, ExpectedAuthorityVersion: saved.AuthorityVersion, RequestID: saved.RequestID, Workflow: saved.Ref, ParametersText: string(parameters), SourceOperationID: saved.SourceOperationID, ExpectedSourceVersion: saved.SourceVersion, AcknowledgeLostNodeTeardown: saved.AcknowledgeLostNodeTeardown, TimeoutSeconds: saved.IntervalSeconds})
	if err != nil {
		return fmt.Errorf("%w; preserve the original native --request-file and input for recovery", err)
	}
	if err = validateNVKENativeReceipt(saved, row); err != nil {
		return err
	}
	return finishNVKERequest(cmd, ctx, c, row)
}
