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

const nvkeCredentialRequestKind = "nvke-credential-observation"
const nvkeCredentialOperationKey = "metrics.credentials"

type nvkeCredentialRequest struct {
	reviewedStartRequest
	AuthorityVersion  int    `json:"authorityVersion"`
	SourceOperationID string `json:"sourceOperationId"`
	SourceVersion     int    `json:"sourceVersion"`
}

func newNVKECredentialRequestCommand(recoverOnly bool) *cobra.Command {
	verb, short := "request", "Collect credentials from a reviewed controller receipt with durable recovery"
	if recoverOnly {
		verb, short = "recover", "Look up the original credential receipt without another admission"
	}
	cmd := &cobra.Command{Use: verb, Args: cobra.NoArgs, Short: short, Long: `Credential observation requests require admin and read NVKE permissions.
The server derives the cluster selector from a successful current controller read;
no cluster name or raw native input is accepted here. The private recovery file
is flushed before admission and contains only scope, versions and request identity.
Reusing it checks the original receipt before current authority or source state.
The recover command only looks up an existing receipt and never re-admits work.
A local wait timeout does not cancel the observation.`, RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
		return runNVKECredentialRequest(cmd, ctx, c, recoverOnly)
	})}
	cmd.Flags().String("request-file", "", "New or original private recovery file (required)")
	_ = cmd.MarkFlagRequired("request-file")
	cmd.Flags().Bool("wait", false, "Wait for the durable observation receipt")
	nvkeWaitFlags(cmd)
	if !recoverOnly {
		nvkeReadSiteFlags(cmd)
		cmd.Flags().Int("site-version", 0, "Reviewed site version; required for a new request")
		cmd.Flags().Int("authority-version", 0, "Reviewed metrics authority version; required for a new request")
		cmd.Flags().String("source-operation", "", "Successful controller get-cluster operation GUID")
		cmd.Flags().Int("source-version", 0, "Reviewed successful source operation version")
		cmd.Flags().Int("operation-timeout", 600, "Observation deadline seconds (30–86400)")
	}
	return cmd
}

func validateNVKECredentialRecovery(row *nvkeCredentialRequest) error {
	if row == nil || row.Format != 1 || row.Kind != nvkeCredentialRequestKind || row.NVKESource != nil || row.Ref != nvkeCredentialOperationKey || row.Version < 1 || row.AuthorityVersion < 1 || row.SourceVersion < 1 || row.ActorUserID < 1 || row.IntervalSeconds < 30 || row.IntervalSeconds > 86400 || nvkeGUID(row.TargetID) != nil || nvkeGUID(row.RequestID) != nil || nvkeGUID(row.SourceOperationID) != nil || !nvkeRevisionPattern.MatchString(row.Revision) || row.InputSchemaDigest != "" || row.ExpectedSource != "" {
		return errors.New("credential recovery metadata invalid; preserve the original private file")
	}
	return nil
}

func readNVKECredentialRecovery(filename string) (*nvkeCredentialRequest, error) {
	data, err := privatefile.ReadFile(filename, 16384)
	if err != nil {
		return nil, err
	}
	var row nvkeCredentialRequest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&row) != nil {
		return nil, errors.New("credential recovery metadata could not be decoded")
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return nil, errors.New("credential recovery file contains extra data")
	}
	if err = validateNVKECredentialRecovery(&row); err != nil {
		return nil, err
	}
	return &row, nil
}

func validateNVKECredentialReceipt(saved *nvkeCredentialRequest, row *api.NVKEOperation) error {
	if row == nil || nvkeGUID(row.ID) != nil || row.Version < 1 || row.SiteID != saved.TargetID || row.RequestID != saved.RequestID || row.OperationKey != nvkeCredentialOperationKey || row.IsInventory || row.ResourceSource != nil {
		return errors.New("credential receipt does not confirm the original request; preserve recovery")
	}
	return nil
}

func runNVKECredentialRequest(cmd *cobra.Command, ctx context.Context, c *api.Client, recoverOnly bool) error {
	filename := resourceStringFlag(cmd, "request-file")
	if filename == "" || filename == "-" {
		return errors.New("use a private credential recovery file path")
	}
	if boolFlag(cmd, "wait") {
		if err := validateNVKEWait(cmd); err != nil {
			return err
		}
	}
	site := resourceStringFlag(cmd, "site")
	if !recoverOnly && nvkeGUID(site) != nil {
		return errors.New("use an exact NVKE site GUID")
	}
	server, actor, err := reviewedRequestScope(cmd, c)
	if err != nil {
		return errors.New("credential caller/server/organization identity unavailable")
	}
	saved, err := readNVKECredentialRecovery(filename)
	if err == nil {
		if err = saved.checkScope(nvkeCredentialRequestKind, server, c.Org(), actor); err != nil {
			return err
		}
		if !recoverOnly && saved.TargetID != site {
			return errors.New("credential recovery belongs to another site")
		}
		row, lookupErr := c.NVKENativeRequestReceipt(ctx, saved.TargetID, saved.RequestID)
		if lookupErr != nil {
			return lookupErr
		}
		if row != nil {
			if err = validateNVKECredentialReceipt(saved, row); err != nil {
				return err
			}
			return finishNVKERequest(cmd, ctx, c, row)
		}
		if recoverOnly {
			return errors.New("no credential receipt is visible; retain the original recovery file before reviewing the same request again")
		}
		for flag, expected := range map[string]int{"site-version": saved.Version, "authority-version": saved.AuthorityVersion, "source-version": saved.SourceVersion, "operation-timeout": saved.IntervalSeconds} {
			actual, _ := cmd.Flags().GetInt(flag)
			if cmd.Flags().Changed(flag) && actual != expected {
				return errors.New("pending credential request cannot change reviewed versions or deadline")
			}
		}
		if cmd.Flags().Changed("source-operation") && resourceStringFlag(cmd, "source-operation") != saved.SourceOperationID {
			return errors.New("pending credential request cannot change its reviewed source")
		}
	} else if !errors.Is(err, os.ErrNotExist) || recoverOnly {
		return errors.New("credential recovery file unavailable; preserve its original private contents")
	} else {
		siteVersion, _ := cmd.Flags().GetInt("site-version")
		authorityVersion, _ := cmd.Flags().GetInt("authority-version")
		sourceVersion, _ := cmd.Flags().GetInt("source-version")
		timeout, _ := cmd.Flags().GetInt("operation-timeout")
		sourceID := resourceStringFlag(cmd, "source-operation")
		if siteVersion < 1 || authorityVersion < 1 || sourceVersion < 1 || nvkeGUID(sourceID) != nil || timeout < 30 || timeout > 86400 {
			return errors.New("new credential requests require reviewed site/authority/source versions, source GUID and deadline 30–86400")
		}
		siteRow, readErr := c.NVKESite(ctx, site)
		if readErr != nil {
			return readErr
		}
		authority, readErr := c.NVKEMetricsAuthority(ctx, site)
		if readErr != nil {
			return readErr
		}
		source, readErr := c.NVKEOperation(ctx, sourceID)
		if readErr != nil {
			return readErr
		}
		if authority == nil || validateNVKEMetricsReply(authority, api.NVKEMetricsReview{SiteID: site}, "get") != nil || !authority.IsEnabled || authority.Version != authorityVersion || authority.SiteVersion != siteVersion || siteRow.Version != siteVersion || !siteRow.IsActive || siteRow.UpstreamRevision != nvkecatalog.Pinned().Revision || source.SiteID != site || source.Version != sourceVersion || source.OperationKey != "getCluster" || source.Status != "SUCCEEDED" || source.IsInventory {
			return errors.New("metrics authority or controller source changed; refresh the review")
		}
		saved = &nvkeCredentialRequest{reviewedStartRequest: reviewedStartRequest{Format: 1, Kind: nvkeCredentialRequestKind, Server: server, OrganizationID: c.Org(), ActorUserID: actor, TargetID: site, TargetName: siteRow.Slug, RequestID: uuid.NewString(), Version: siteVersion, Ref: nvkeCredentialOperationKey, Revision: siteRow.UpstreamRevision, IntervalSeconds: timeout}, AuthorityVersion: authorityVersion, SourceOperationID: sourceID, SourceVersion: sourceVersion}
		if err = validateNVKECredentialRecovery(saved); err != nil {
			return err
		}
		data, encodeErr := json.MarshalIndent(saved, "", "  ")
		if encodeErr != nil {
			return errors.New("credential recovery metadata could not be encoded")
		}
		if err = writeNVKEExclusive(filename, append(data, '\n')); err != nil {
			return err
		}
	}
	persisted, err := readNVKECredentialRecovery(filename)
	if err != nil || !reflect.DeepEqual(saved, persisted) {
		return errors.New("credential recovery identity changed before submission")
	}
	row, err := c.RequestNVKECredentialObservation(ctx, api.NVKECredentialRequestInput{SiteID: saved.TargetID, ExpectedActorID: saved.ActorUserID, ExpectedSiteVersion: saved.Version, ExpectedAuthorityVersion: saved.AuthorityVersion, SourceOperationID: saved.SourceOperationID, ExpectedSourceVersion: saved.SourceVersion, RequestID: saved.RequestID, TimeoutSeconds: saved.IntervalSeconds})
	if err != nil {
		return fmt.Errorf("%w; preserve the original metrics --request-file for recovery", err)
	}
	if err = validateNVKECredentialReceipt(saved, row); err != nil {
		return err
	}
	return finishNVKERequest(cmd, ctx, c, row)
}
