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

const nvkeAgentRequestKind = "nvke-operator-agent"

type nvkeAgentRequest struct {
	reviewedStartRequest
	AuthorityVersion                int  `json:"authorityVersion"`
	AcknowledgeInfrastructureChange bool `json:"acknowledgeInfrastructureChange"`
}

func newNVKEAgentRequestCommand(recoverOnly bool) *cobra.Command {
	verb, short := "request", "Submit a complete native command envelope with private durable recovery"
	if recoverOnly {
		verb, short = "recover", "Look up the original operator receipt without another admission"
	}
	cmd := &cobra.Command{Use: verb, Args: cobra.NoArgs, Short: short, Long: `Send the native request as private UTF-8 JSON text. All declared positional inputs,
options and artifact references remain available. The server validates the full envelope.
Recovery metadata is flushed before admission and contains only scope, request identity,
reviewed versions and a digest of the private input. Recover never re-admits work.
A lost response or local wait expiry does not authorize another native invocation.`, RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
		return runNVKEAgentRequest(cmd, ctx, c, recoverOnly)
	})}
	cmd.Flags().String("request-file", "", "New or original private recovery file (required)")
	_ = cmd.MarkFlagRequired("request-file")
	cmd.Flags().Bool("wait", false, "Wait for the original durable operation; local timeout does not cancel remote work")
	nvkeWaitFlags(cmd)
	if !recoverOnly {
		nvkeReadSiteFlags(cmd)
		cmd.Flags().String("file", "", "Private full native request JSON file or - for stdin; required before admission")
		cmd.Flags().Int("site-version", 0, "Reviewed site version; required for a new request")
		cmd.Flags().Int("authority-version", 0, "Reviewed verified operator agent version; required for a new request")
		cmd.Flags().Int("operation-timeout", 7200, "Server observation deadline seconds (30–86400)")
		cmd.Flags().Bool("acknowledge-infrastructure-change", false, "Explicitly acknowledge reviewed bootstrap/install/access changes")
	}
	return cmd
}
func readNVKEAgentRecovery(filename string) (*nvkeAgentRequest, error) {
	data, err := privatefile.ReadFile(filename, 16384)
	if err != nil {
		return nil, err
	}
	var row nvkeAgentRequest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&row) != nil {
		return nil, errors.New("operator recovery metadata could not be decoded")
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return nil, errors.New("operator recovery file contains extra data")
	}
	if row.Format != 1 || row.Kind != nvkeAgentRequestKind || row.NVKESource != nil || row.InputSchemaDigest != "" || row.ActorUserID < 1 || row.Version < 1 || row.AuthorityVersion < 1 || row.IntervalSeconds < 30 || row.IntervalSeconds > 86400 || nvkeGUID(row.TargetID) != nil || nvkeGUID(row.RequestID) != nil || !nvkeDigestPattern.MatchString(row.ExpectedSource) || !nvkeRevisionPattern.MatchString(row.Revision) || !strings.HasPrefix(row.Ref, "tooling.") || len(row.Ref) <= 7 || len(row.Ref) > 128 {
		return nil, errors.New("operator recovery metadata invalid; preserve the original private file")
	}
	return &row, nil
}
func validateNVKEAgentReceipt(saved *nvkeAgentRequest, row *api.NVKEOperation) error {
	if row == nil || nvkeGUID(row.ID) != nil || row.Version < 1 || row.SiteID != saved.TargetID || row.RequestID != saved.RequestID || row.OperationKey != saved.Ref || row.IsInventory || row.ResourceSource != nil || row.ProviderSource != nil {
		return errors.New("operator receipt does not confirm the original request; preserve recovery")
	}
	return nil
}
func runNVKEAgentRequest(cmd *cobra.Command, ctx context.Context, c *api.Client, recoverOnly bool) error {
	filename := resourceStringFlag(cmd, "request-file")
	if filename == "" || filename == "-" {
		return errors.New("use a private operator recovery file path")
	}
	if boolFlag(cmd, "wait") {
		if err := validateNVKEWait(cmd); err != nil {
			return err
		}
	}
	site := resourceStringFlag(cmd, "site")
	if !recoverOnly && nvkeGUID(site) != nil {
		return errors.New("use an exact site GUID")
	}
	server, actor, err := reviewedRequestScope(cmd, c)
	if err != nil {
		return errors.New("operator caller/server/organization identity unavailable")
	}
	saved, err := readNVKEAgentRecovery(filename)
	if err == nil {
		if err = saved.checkScope(nvkeAgentRequestKind, server, c.Org(), actor); err != nil {
			return err
		}
		if !recoverOnly && saved.TargetID != site {
			return errors.New("operator recovery belongs to another site")
		}
		row, lookupErr := c.NVKENativeRequestReceipt(ctx, saved.TargetID, saved.RequestID)
		if lookupErr != nil {
			return lookupErr
		}
		if row != nil {
			if err = validateNVKEAgentReceipt(saved, row); err != nil {
				return err
			}
			return finishNVKERequest(cmd, ctx, c, row)
		}
		if recoverOnly {
			return errors.New("original operator receipt not visible; preserve recovery before reviewing the same request")
		}
		for flag, want := range map[string]int{"site-version": saved.Version, "authority-version": saved.AuthorityVersion, "operation-timeout": saved.IntervalSeconds} {
			actual, _ := cmd.Flags().GetInt(flag)
			if cmd.Flags().Changed(flag) && actual != want {
				return errors.New("pending operator request cannot change reviewed versions or deadline")
			}
		}
		if cmd.Flags().Changed("acknowledge-infrastructure-change") && boolFlag(cmd, "acknowledge-infrastructure-change") != saved.AcknowledgeInfrastructureChange {
			return errors.New("pending operator request cannot change its infrastructure acknowledgement")
		}
	} else if !errors.Is(err, os.ErrNotExist) || recoverOnly {
		return errors.New("operator recovery file unavailable; preserve its original private contents")
	}
	inputFile := resourceStringFlag(cmd, "file")
	if inputFile == "" {
		return errors.New("supply the private native envelope with --file before admission")
	}
	text, err := readNVKEText(cmd, inputFile)
	if err != nil {
		return err
	}
	if len(text) > 1024*1024 {
		return errors.New("operator request exceeds the 1 MiB limit")
	}
	if saved != nil {
		if saved.ExpectedSource != nvkeParameterDigest(text) {
			return errors.New("pending operator request requires identical original private text")
		}
	} else {
		siteVersion, _ := cmd.Flags().GetInt("site-version")
		authorityVersion, _ := cmd.Flags().GetInt("authority-version")
		timeout, _ := cmd.Flags().GetInt("operation-timeout")
		if siteVersion < 1 || authorityVersion < 1 || timeout < 30 || timeout > 86400 {
			return errors.New("new operator requests require reviewed positive site/authority versions and deadline 30–86400")
		}
		siteRow, readErr := c.NVKESite(ctx, site)
		if readErr != nil {
			return readErr
		}
		selected, readErr := c.NVKEAgentAuthority(ctx, site)
		if readErr != nil {
			return readErr
		}
		catalog, readErr := c.NVKEAgentCatalog(ctx)
		if readErr != nil {
			return readErr
		}
		if validateNVKEAgentReply(selected, api.NVKEAgentReview{SiteID: site}, "get") != nil || !selected.IsEnabled || selected.VerifiedAt == nil || selected.VerificationCode != "AGENT_VERIFIED" || selected.Version != authorityVersion || selected.SiteVersion != siteVersion || siteRow.Version != siteVersion || !siteRow.IsActive || siteRow.UpstreamRevision != nvkecatalog.Pinned().Revision || catalog.Revision != siteRow.UpstreamRevision {
			return errors.New("operator authority, site or pinned revision changed; refresh the review")
		}
		var envelope struct {
			Format   int                        `json:"format"`
			Revision string                     `json:"revision"`
			SiteID   string                     `json:"siteId"`
			Command  string                     `json:"command"`
			Options  map[string]json.RawMessage `json:"options"`
		}
		if json.Unmarshal(text, &envelope) != nil || envelope.Format != 1 || envelope.Revision != catalog.Revision || envelope.SiteID != site {
			return errors.New("review a complete pinned operator envelope for this site")
		}
		var definition *api.NVKEAgentCommand
		for i := range catalog.Commands {
			if catalog.Commands[i].Key == envelope.Command {
				definition = &catalog.Commands[i]
				break
			}
		}
		acknowledged := boolFlag(cmd, "acknowledge-infrastructure-change")
		if definition == nil {
			return errors.New("operator command is absent from the connected server catalog")
		}
		if definition.InfrastructureChange && strings.TrimSpace(string(envelope.Options["dry-run"])) != "true" && !acknowledged {
			return errors.New("review the infrastructure change and use --acknowledge-infrastructure-change before admission")
		}
		saved = &nvkeAgentRequest{reviewedStartRequest: reviewedStartRequest{Format: 1, Kind: nvkeAgentRequestKind, Server: server, OrganizationID: c.Org(), ActorUserID: actor, TargetID: site, TargetName: siteRow.Slug, RequestID: uuid.NewString(), Version: siteVersion, Ref: "tooling." + envelope.Command, Revision: catalog.Revision, IntervalSeconds: timeout, ExpectedSource: nvkeParameterDigest(text)}, AuthorityVersion: authorityVersion, AcknowledgeInfrastructureChange: acknowledged}
		data, encodeErr := json.MarshalIndent(saved, "", "  ")
		if encodeErr != nil {
			return errors.New("operator recovery metadata could not be encoded")
		}
		if err = writeNVKEExclusive(filename, append(data, '\n')); err != nil {
			return err
		}
	}
	persisted, err := readNVKEAgentRecovery(filename)
	if err != nil || !reflect.DeepEqual(saved, persisted) {
		return errors.New("operator recovery identity changed before admission")
	}
	row, err := c.RequestNVKEAgentJob(ctx, api.NVKEAgentRequestInput{SiteID: saved.TargetID, ExpectedActorID: saved.ActorUserID, ExpectedSiteVersion: saved.Version, ExpectedAuthorityVersion: saved.AuthorityVersion, RequestID: saved.RequestID, RequestText: string(text), AcknowledgeInfrastructureChange: saved.AcknowledgeInfrastructureChange, TimeoutSeconds: saved.IntervalSeconds})
	if err != nil {
		return fmt.Errorf("%w; preserve original --request-file and private input for recovery", err)
	}
	if err = validateNVKEAgentReceipt(saved, row); err != nil {
		return err
	}
	return finishNVKERequest(cmd, ctx, c, row)
}
