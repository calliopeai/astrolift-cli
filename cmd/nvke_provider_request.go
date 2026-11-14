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

const nvkeProviderRequestKind = "nvke-provider-observation"
const nvkeProviderOperationKey = "infrastructure.providers"

type nvkeProviderRequest struct {
	reviewedStartRequest
	ConnectionID      string `json:"connectionId"`
	ConnectionVersion int    `json:"connectionVersion"`
}

func newNVKEProviderRequestCommand(recoverOnly bool) *cobra.Command {
	verb, short := "request", "Collect providers through a reviewed management-Kubernetes connection with durable recovery"
	if recoverOnly {
		verb, short = "recover", "Look up the original provider receipt without another admission"
	}
	cmd := &cobra.Command{Use: verb, Args: cobra.NoArgs, Short: short, Long: `Provider observation requests require admin and read NVKE permissions.
The explicit management-Kubernetes connection GUID and version are reviewed;
no native request fields or provider credentials are accepted here. The private recovery file
is flushed before admission and contains only scope, versions and request identity.
Reusing it checks the original receipt before current authority or source state.
The recover command only looks up an existing receipt and never re-admits work.
A local wait timeout does not cancel the observation.`, RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
		return runNVKEProviderRequest(cmd, ctx, c, recoverOnly)
	})}
	cmd.Flags().String("request-file", "", "New or original private recovery file (required)")
	_ = cmd.MarkFlagRequired("request-file")
	cmd.Flags().Bool("wait", false, "Wait for the durable observation receipt")
	nvkeWaitFlags(cmd)
	if !recoverOnly {
		nvkeReadSiteFlags(cmd)
		cmd.Flags().Int("site-version", 0, "Reviewed site version; required for a new request")
		cmd.Flags().String("connection", "", "Reviewed explicit management-Kubernetes connection GUID")
		cmd.Flags().Int("connection-version", 0, "Reviewed management-Kubernetes connection version")
		cmd.Flags().Int("operation-timeout", 600, "Observation deadline seconds (30–86400)")
	}
	return cmd
}

func validateNVKEProviderRecovery(row *nvkeProviderRequest) error {
	if row == nil || row.Format != 1 || row.Kind != nvkeProviderRequestKind || row.NVKESource != nil || row.Ref != nvkeProviderOperationKey || row.Version < 1 || row.ConnectionVersion < 1 || row.ActorUserID < 1 || row.IntervalSeconds < 30 || row.IntervalSeconds > 86400 || nvkeGUID(row.TargetID) != nil || nvkeGUID(row.RequestID) != nil || nvkeGUID(row.ConnectionID) != nil || !nvkeRevisionPattern.MatchString(row.Revision) || row.TargetName != "" || row.InputSchemaDigest != "" || row.ExpectedSource != "" {
		return errors.New("provider recovery metadata invalid; preserve the original private file")
	}
	return nil
}

func readNVKEProviderRecovery(filename string) (*nvkeProviderRequest, error) {
	data, err := privatefile.ReadFile(filename, 16384)
	if err != nil {
		return nil, err
	}
	decoderTokens := json.NewDecoder(strings.NewReader(string(data)))
	token, tokenErr := decoderTokens.Token()
	if tokenErr != nil || token != json.Delim('{') {
		return nil, errors.New("provider recovery must contain one metadata object")
	}
	seen := map[string]bool{}
	for decoderTokens.More() {
		key, keyErr := decoderTokens.Token()
		name, ok := key.(string)
		if keyErr != nil || !ok || seen[name] {
			return nil, errors.New("provider recovery contains duplicate or invalid fields")
		}
		seen[name] = true
		var raw json.RawMessage
		if decoderTokens.Decode(&raw) != nil {
			return nil, errors.New("provider recovery contains invalid fields")
		}
	}
	var row nvkeProviderRequest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&row) != nil {
		return nil, errors.New("provider recovery metadata could not be decoded")
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return nil, errors.New("provider recovery file contains extra data")
	}
	if err = validateNVKEProviderRecovery(&row); err != nil {
		return nil, err
	}
	return &row, nil
}

func validateNVKEProviderReceipt(saved *nvkeProviderRequest, row *api.NVKEOperation) error {
	if row == nil || nvkeGUID(row.ID) != nil || row.Version < 1 || row.SiteID != saved.TargetID || row.RequestID != saved.RequestID || row.OperationKey != nvkeProviderOperationKey || row.IsInventory || row.ResourceSource != nil || row.ProviderSource == nil || row.ProviderSource.ConnectionID != saved.ConnectionID || row.ProviderSource.ConnectionVersion != saved.ConnectionVersion || row.ProviderSource.SiteVersion != saved.Version || row.ProviderSource.UpstreamRevision != saved.Revision {
		return errors.New("provider receipt does not confirm the original request; preserve recovery")
	}
	return nil
}

func runNVKEProviderRequest(cmd *cobra.Command, ctx context.Context, c *api.Client, recoverOnly bool) error {
	filename := resourceStringFlag(cmd, "request-file")
	if filename == "" || filename == "-" {
		return errors.New("use a private provider recovery file path")
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
		return errors.New("provider caller/server/organization identity unavailable")
	}
	saved, err := readNVKEProviderRecovery(filename)
	if err == nil {
		if err = saved.checkScope(nvkeProviderRequestKind, server, c.Org(), actor); err != nil {
			return err
		}
		if !recoverOnly && saved.TargetID != site {
			return errors.New("provider recovery belongs to another site")
		}
		row, lookupErr := c.NVKENativeRequestReceipt(ctx, saved.TargetID, saved.RequestID)
		if lookupErr != nil {
			return lookupErr
		}
		if row != nil {
			if err = validateNVKEProviderReceipt(saved, row); err != nil {
				return err
			}
			return finishNVKERequest(cmd, ctx, c, row)
		}
		if recoverOnly {
			return errors.New("no provider receipt is visible; retain the original recovery file before reviewing the same request again")
		}
		for flag, expected := range map[string]int{"site-version": saved.Version, "connection-version": saved.ConnectionVersion, "operation-timeout": saved.IntervalSeconds} {
			actual, _ := cmd.Flags().GetInt(flag)
			if cmd.Flags().Changed(flag) && actual != expected {
				return errors.New("pending provider request cannot change reviewed versions or deadline")
			}
		}
		if cmd.Flags().Changed("connection") && resourceStringFlag(cmd, "connection") != saved.ConnectionID {
			return errors.New("pending provider request cannot change its reviewed source")
		}
	} else if !errors.Is(err, os.ErrNotExist) || recoverOnly {
		return errors.New("provider recovery file unavailable; preserve its original private contents")
	} else {
		siteVersion, _ := cmd.Flags().GetInt("site-version")
		connectionVersion, _ := cmd.Flags().GetInt("connection-version")
		timeout, _ := cmd.Flags().GetInt("operation-timeout")
		connectionID := resourceStringFlag(cmd, "connection")
		if siteVersion < 1 || connectionVersion < 1 || nvkeGUID(connectionID) != nil || timeout < 30 || timeout > 86400 {
			return errors.New("new provider requests require reviewed site/connection versions, connection GUID and deadline 30–86400")
		}
		siteRow, readErr := c.NVKESite(ctx, site)
		if readErr != nil {
			return readErr
		}

		var connection *api.NVKEConnection
		for i := range siteRow.Connections {
			row := &siteRow.Connections[i]
			if row.ID == connectionID && row.Surface == "DATASTORE" && row.IsEnabled {
				connection = row
			}
		}
		if connection == nil || connection.Version != connectionVersion || siteRow.Version != siteVersion || !siteRow.IsActive || siteRow.UpstreamRevision != nvkecatalog.Pinned().Revision {
			return errors.New("provider connection or site authority changed; refresh the review")
		}
		saved = &nvkeProviderRequest{reviewedStartRequest: reviewedStartRequest{Format: 1, Kind: nvkeProviderRequestKind, Server: server, OrganizationID: c.Org(), ActorUserID: actor, TargetID: site, RequestID: uuid.NewString(), Version: siteVersion, Ref: nvkeProviderOperationKey, Revision: siteRow.UpstreamRevision, IntervalSeconds: timeout}, ConnectionID: connectionID, ConnectionVersion: connectionVersion}
		if err = validateNVKEProviderRecovery(saved); err != nil {
			return err
		}
		data, encodeErr := json.MarshalIndent(saved, "", "  ")
		if encodeErr != nil {
			return errors.New("provider recovery metadata could not be encoded")
		}
		if err = writeNVKEExclusive(filename, append(data, '\n')); err != nil {
			return err
		}
	}
	persisted, err := readNVKEProviderRecovery(filename)
	if err != nil || !reflect.DeepEqual(saved, persisted) {
		return errors.New("provider recovery identity changed before submission")
	}
	row, err := c.RequestNVKEProviderObservation(ctx, api.NVKEProviderRequestInput{SiteID: saved.TargetID, ExpectedActorID: saved.ActorUserID, ExpectedSiteVersion: saved.Version, ConnectionID: saved.ConnectionID, ExpectedConnectionVersion: saved.ConnectionVersion, RequestID: saved.RequestID, TimeoutSeconds: saved.IntervalSeconds})
	if err != nil {
		return fmt.Errorf("%w; preserve the original providers --request-file for recovery", err)
	}
	if err = validateNVKEProviderReceipt(saved, row); err != nil {
		return err
	}
	return finishNVKERequest(cmd, ctx, c, row)
}
