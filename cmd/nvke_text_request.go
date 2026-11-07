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
	"unicode/utf8"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/nvkecatalog"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const nvkeTextRequestKind = "nvke-operation-text"
const nvkeInventoryRequestKind = "nvke-inventory"
const nvkeResourceRequestKind = "nvke-resource"

func nvkeSourceFlags(cmd *cobra.Command) {
	cmd.Flags().String("source-operation", "", "Successful reviewed resource observation GUID")
	cmd.Flags().Int("source-version", 0, "Exact reviewed source operation version")
	cmd.Flags().String("resource-key", "", "Signed inventory row key; omit for a single-resource GET source")
}
func nvkeSourceFromFlags(cmd *cobra.Command) (*api.NVKEResourceSource, error) {
	id := resourceStringFlag(cmd, "source-operation")
	version, _ := cmd.Flags().GetInt("source-version")
	key := resourceStringFlag(cmd, "resource-key")
	if !cmd.Flags().Changed("source-operation") && !cmd.Flags().Changed("source-version") && !cmd.Flags().Changed("resource-key") {
		return nil, nil
	}
	source := &api.NVKEResourceSource{OperationID: id, Version: version}
	if cmd.Flags().Changed("resource-key") {
		source.ResourceKey = &key
	}
	return source, validateNVKESource(source)
}
func validateNVKESource(source *api.NVKEResourceSource) error {
	if source == nil || nvkeGUID(source.OperationID) != nil || source.Version < 1 || (source.ResourceKey != nil && (len(*source.ResourceKey) == 0 || len(*source.ResourceKey) > 4096)) {
		return errors.New("use a canonical --source-operation, positive --source-version and optional nonempty signed --resource-key")
	}
	return nil
}
func sameNVKESource(a, b *api.NVKEResourceSource) bool {
	return reflect.DeepEqual(a, b)
}
func validateNVKETextRecovery(r *reviewedStartRequest) error {
	if r.Format != 1 || r.Version < 1 || r.ActorUserID < 1 || r.Ref == "" || len(r.Ref) > 128 || r.IntervalSeconds < 30 || r.IntervalSeconds > 86400 || !nvkeDigestPattern.MatchString(r.ExpectedSource) || !nvkeRevisionPattern.MatchString(r.Revision) || nvkeGUID(r.TargetID) != nil || nvkeGUID(r.RequestID) != nil {
		return errors.New("NVKE recovery metadata is invalid; preserve the original file")
	}
	switch r.Kind {
	case nvkeTextRequestKind, nvkeInventoryRequestKind:
		if r.NVKESource != nil {
			return errors.New("ordinary and inventory requests cannot replace a resource-bound recovery")
		}
	case nvkeResourceRequestKind:
		return validateNVKESource(r.NVKESource)
	default:
		return errors.New("recovery file belongs to another command")
	}
	return nil
}
func readNVKEText(cmd *cobra.Command, filename string) ([]byte, error) {
	var data []byte
	var err error
	if filename == "-" {
		data, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), nvkeInputLimit+1))
	} else {
		data, err = privatefile.ReadFile(filename, nvkeInputLimit)
	}
	if err != nil || int64(len(data)) > nvkeInputLimit || !utf8.Valid(data) {
		return nil, errors.New("parameters must be a bounded private UTF-8 JSON file or stdin")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var object map[string]any
	if decoder.Decode(&object) != nil || object == nil {
		return nil, errors.New("parameters must contain one JSON object")
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return nil, errors.New("parameters must contain one JSON object without trailing data")
	}
	return data, nil
}
func validateNVKEReceipt(saved *reviewedStartRequest, row *api.NVKEOperation) error {
	inventory := saved.Kind == nvkeInventoryRequestKind
	if row == nil || nvkeGUID(row.ID) != nil || row.SiteID != saved.TargetID || row.RequestID != saved.RequestID || row.OperationKey != saved.Ref || row.IsInventory != inventory || !sameNVKESource(row.ResourceSource, saved.NVKESource) {
		return errors.New("NVKE receipt did not confirm the original mode, source and identity; preserve recovery")
	}
	return nil
}
func finishNVKERequest(cmd *cobra.Command, ctx context.Context, c *api.Client, row *api.NVKEOperation) error {
	if boolFlag(cmd, "wait") {
		return waitNVKEOperation(cmd, ctx, c, row)
	}
	return nvkePrintOperation(cmd, row)
}
func runNVKERequest(cmd *cobra.Command, ctx context.Context, c *api.Client, key string, pinned bool) error {
	saved, err := readReviewedRequest(resourceStringFlag(cmd, "request-file"))
	if err == nil && saved.Kind == nvkeRequestKind {
		if boolFlag(cmd, "inventory") || resourceStringFlag(cmd, "source-operation") != "" || cmd.Flags().Changed("source-version") || cmd.Flags().Changed("resource-key") {
			return errors.New("legacy recovery cannot change mode or reviewed source")
		}
		return runNVKELegacyRequest(cmd, ctx, c, key, pinned)
	}
	return runNVKETextRequest(cmd, ctx, c, key, pinned)
}
func runNVKETextRequest(cmd *cobra.Command, ctx context.Context, c *api.Client, key string, pinned bool) error {
	siteID := resourceStringFlag(cmd, "site")
	filename := resourceStringFlag(cmd, "request-file")
	timeout, _ := cmd.Flags().GetInt("operation-timeout")
	version, _ := cmd.Flags().GetInt("site-version")
	if nvkeGUID(siteID) != nil || filename == "" || filename == "-" || timeout < 30 || timeout > 86400 || version < 0 {
		return errors.New("use an exact site GUID, private recovery path, valid timeout and site version")
	}
	if boolFlag(cmd, "wait") {
		if err := validateNVKEWait(cmd); err != nil {
			return err
		}
	}
	source, err := nvkeSourceFromFlags(cmd)
	if err != nil {
		return err
	}
	kind := nvkeTextRequestKind
	if boolFlag(cmd, "inventory") {
		kind = nvkeInventoryRequestKind
	}
	if source != nil {
		if kind == nvkeInventoryRequestKind {
			return errors.New("inventory and resource-source modes are separate")
		}
		kind = nvkeResourceRequestKind
	}
	server, actor, err := reviewedRequestScope(cmd, c)
	if err != nil {
		return errors.New("NVKE caller/server/organization identity could not be verified")
	}
	saved, err := readReviewedRequest(filename)
	if err == nil {
		if err = validateNVKETextRecovery(saved); err != nil {
			return err
		}
		if err = saved.checkScope(saved.Kind, server, c.Org(), actor); err != nil {
			return err
		}
		if saved.TargetID != siteID || saved.Ref != key {
			return errors.New("recovery file belongs to another site or action; preserve its original scope")
		}
		receipt, lookupErr := c.NVKERequest(ctx, siteID, saved.RequestID)
		if lookupErr != nil {
			return lookupErr
		}
		if receipt != nil {
			if err = validateNVKEReceipt(saved, receipt); err != nil {
				return err
			}
			return finishNVKERequest(cmd, ctx, c, receipt)
		}
		if source == nil && !cmd.Flags().Changed("inventory") {
			kind = saved.Kind
			source = saved.NVKESource
		}
		if saved.Kind != kind || !sameNVKESource(saved.NVKESource, source) || (cmd.Flags().Changed("site-version") && saved.Version != version) || (cmd.Flags().Changed("operation-timeout") && saved.IntervalSeconds != timeout) {
			return errors.New("pending request cannot change its original mode, source, version or timeout")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("NVKE recovery file is unavailable; preserve it and correct private file protections")
	}
	parameters := []byte("{}")
	if input := resourceStringFlag(cmd, "file"); input != "" {
		parameters, err = readNVKEText(cmd, input)
		if err != nil {
			return err
		}
	}
	if saved != nil {
		if saved.ExpectedSource != nvkeParameterDigest(parameters) {
			return errors.New("pending request requires the identical original parameter text")
		}
	} else {
		site, siteErr := c.NVKESite(ctx, siteID)
		if siteErr != nil {
			return siteErr
		}
		contract, contractErr := c.NVKEContract(ctx)
		if contractErr != nil {
			return contractErr
		}
		allowed := false
		for _, a := range contract.Actions {
			if a.Key == key {
				allowed = true
			}
		}
		if !allowed || !site.IsActive || site.Version < 1 || site.UpstreamRevision != contract.Revision || !nvkeRevisionPattern.MatchString(contract.Revision) || (pinned && contract.Revision != nvkecatalog.Pinned().Revision) || (version > 0 && site.Version != version) {
			return errors.New("NVKE action, site or pinned revision requires review")
		}
		saved = &reviewedStartRequest{Format: 1, Kind: kind, Server: server, OrganizationID: c.Org(), ActorUserID: actor, TargetID: site.ID, TargetName: site.Slug, RequestID: uuid.NewString(), Version: site.Version, Ref: key, Revision: contract.Revision, IntervalSeconds: timeout, ExpectedSource: nvkeParameterDigest(parameters), NVKESource: source}
		if err = validateNVKETextRecovery(saved); err != nil {
			return err
		}
		if err = createReviewedRequest(filename, *saved); err != nil {
			return errors.New("could not persist recovery identity before admission; preserve any partial file")
		}
	}
	persisted, err := readReviewedRequest(filename)
	if err != nil || !reflect.DeepEqual(saved, persisted) {
		return errors.New("recovery identity changed before admission; no new request sent")
	}
	input := api.NVKERequestTextInput{SiteID: saved.TargetID, ExpectedSiteVersion: saved.Version, ExpectedActorID: saved.ActorUserID, RequestID: saved.RequestID, OperationKey: saved.Ref, ParametersText: string(parameters), TimeoutSeconds: saved.IntervalSeconds}
	var row *api.NVKEOperation
	if saved.Kind == nvkeResourceRequestKind {
		row, err = c.RequestNVKEResource(ctx, api.NVKEResourceRequestInput{NVKERequestTextInput: input, SourceOperationID: saved.NVKESource.OperationID, ExpectedSourceVersion: saved.NVKESource.Version, SourceResourceKey: saved.NVKESource.ResourceKey})
	} else {
		row, err = c.RequestNVKEText(ctx, input, saved.Kind == nvkeInventoryRequestKind)
	}
	if err != nil {
		return fmt.Errorf("%w; preserve --request-file and original text for recovery", err)
	}
	if err = validateNVKEReceipt(saved, row); err != nil {
		return err
	}
	return finishNVKERequest(cmd, ctx, c, row)
}
