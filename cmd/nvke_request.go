package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/nvkecatalog"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const nvkeRequestKind = "nvke-operation"
const nvkeInputLimit int64 = 16 * 1024 * 1024

var nvkeDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var nvkeRevisionPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

func readNVKEJSON(cmd *cobra.Command, filename string, limit int64) ([]byte, error) {
	var data []byte
	var err error
	if filename == "-" {
		data, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), limit+1))
	} else {
		data, err = privatefile.ReadFile(filename, limit)
	}
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("input must be a bounded private JSON file (mode 0600 or protected Windows ACL), or stdin")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var object map[string]any
	if decoder.Decode(&object) != nil || object == nil {
		return nil, errors.New("input must contain one JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("input must contain one JSON object without trailing data")
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, errors.New("input cannot be encoded as JSON")
	}
	return canonical, nil
}
func decodeNVKEStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
func writeNVKEExclusive(filename string, data []byte) error {
	file, err := privatefile.CreateExclusive(filename)
	if err != nil {
		return errors.New("private output path must be new; existing files are never replaced")
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return errors.New("private output could not be flushed; preserve the partial file")
	}
	if closeErr != nil {
		return errors.New("private output could not be closed; preserve the file")
	}
	return privatefile.SyncParent(filename)
}
func nvkeParameterDigest(parameters []byte) string {
	digest := sha256.Sum256(parameters)
	return hex.EncodeToString(digest[:])
}
func validateNVKERecovery(r *reviewedStartRequest) error {
	if r.Format != 1 || r.Kind != nvkeRequestKind || r.Version < 1 || r.IntervalSeconds < 30 || r.IntervalSeconds > 86400 || r.ActorUserID < 1 || r.Ref == "" || !nvkeDigestPattern.MatchString(r.ExpectedSource) || !nvkeRevisionPattern.MatchString(r.Revision) {
		return errors.New("NVKE recovery file lacks its original reviewed tuple; preserve it for investigation")
	}
	if err := nvkeGUID(r.TargetID); err != nil {
		return err
	}
	if err := nvkeGUID(r.RequestID); err != nil {
		return err
	}
	return nil
}
func newNVKERequestCommand(use, fixedKey string) *cobra.Command {
	args := cobra.NoArgs
	if fixedKey == "" {
		args = cobra.ExactArgs(1)
	}
	command := &cobra.Command{Use: use, Short: "Submit an exact native operation with a durable recovery identity", Long: `Submit an NVKE operation through the Astrolift API. --file contains the complete
native JSON parameter envelope: HTTP operations use path/query/body/resource_version;
datastore operations use name/document/resource_version/uid and their declared options.
Omit --file for an empty parameter object. Use catalog to inspect all native fields.

--request-file is created privately and flushed before admission. It stores only the
server, organization, actor, site/version, action, revision, timeout, parameters digest
and request UUID. Native inputs and credentials are not saved in that recovery record.
After a lost reply, retry with the same recovery file and identical original input.
Do not replace the file or refresh its reviewed version while an outcome is unknown.

Requests return metadata, never sensitive native results. Use operation result with
an explicit private output file after inspecting status. --wait waits for observed
completion; a local wait timeout does not cancel the server operation.`, Args: args, RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, arguments []string) error {
		key := fixedKey
		if key == "" {
			key = arguments[0]
		}
		return runNVKERequest(cmd, ctx, c, key, fixedKey != "")
	})}
	command.Flags().String("site", "", "Exact site GUID (required)")
	_ = command.MarkFlagRequired("site")
	command.Flags().String("file", "", "Private JSON parameters file, or - for stdin (default {})")
	command.Flags().String("request-file", "", "New or existing private recovery file (required)")
	_ = command.MarkFlagRequired("request-file")
	command.Flags().Int("site-version", 0, "Assert a specific site version on first admission; recovery retains the original")
	command.Flags().Int("operation-timeout", 7200, "Server operation timeout in seconds (30–86400)")
	command.Flags().Bool("wait", false, "Wait for observed completion after admission")
	nvkeWaitFlags(command)
	return command
}
func runNVKERequest(cmd *cobra.Command, ctx context.Context, c *api.Client, key string, pinnedCommand bool) error {
	siteID := resourceStringFlag(cmd, "site")
	if err := nvkeGUID(siteID); err != nil {
		return err
	}
	filename := resourceStringFlag(cmd, "request-file")
	if filename == "" || filename == "-" {
		return errors.New("--request-file requires a real private recovery path")
	}
	timeout, _ := cmd.Flags().GetInt("operation-timeout")
	version, _ := cmd.Flags().GetInt("site-version")
	if timeout < 30 || timeout > 86400 || version < 0 {
		return errors.New("use operation-timeout 30–86400 and a nonnegative site-version")
	}
	if boolFlag(cmd, "wait") {
		if err := validateNVKEWait(cmd); err != nil {
			return err
		}
	}
	parameters := []byte("{}")
	if input := resourceStringFlag(cmd, "file"); input != "" {
		var err error
		parameters, err = readNVKEJSON(cmd, input, nvkeInputLimit)
		if err != nil {
			return err
		}
	}
	// Identity lookup is read-only and verifies the selected organization. Do not
	// admit any write until the original recovery tuple is durably saved.
	server, actor, err := reviewedRequestScope(cmd, c)
	if err != nil {
		return errors.New("NVKE caller/server/organization identity could not be verified")
	}
	saved, err := readReviewedRequest(filename)
	if err == nil {
		if err = validateNVKERecovery(saved); err != nil {
			return err
		}
		if err = saved.checkScope(nvkeRequestKind, server, c.Org(), actor); err != nil {
			return err
		}
		if saved.TargetID != siteID || saved.Ref != key || saved.ExpectedSource != nvkeParameterDigest(parameters) || (cmd.Flags().Changed("site-version") && saved.Version != version) || (cmd.Flags().Changed("operation-timeout") && saved.IntervalSeconds != timeout) {
			return errors.New("NVKE request differs from its saved site, action, parameters, version or timeout; preserve the original recovery file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("NVKE recovery file could not be read; preserve it and correct its private file protections")
	} else {
		site, err := c.NVKESite(ctx, siteID)
		if err != nil {
			return err
		}
		contract, err := c.NVKEContract(ctx)
		if err != nil {
			return err
		}
		allowed := false
		for _, selected := range contract.Actions {
			if selected.Key == key {
				allowed = true
			}
		}
		if !allowed {
			return errors.New("operation is absent from the connected server's NVKE catalog")
		}
		if site.ID != siteID || !site.IsActive || site.Version < 1 || !nvkeRevisionPattern.MatchString(site.UpstreamRevision) || site.UpstreamRevision != contract.Revision {
			return errors.New("NVKE site is disabled, changed or requires contract review")
		}
		if pinnedCommand && contract.Revision != nvkecatalog.Pinned().Revision {
			return errors.New("native command revision differs from the server; inspect catalog before using operation request")
		}
		if version > 0 && version != site.Version {
			return errors.New("NVKE site version changed before review")
		}
		saved = &reviewedStartRequest{Format: 1, Kind: nvkeRequestKind, Server: server, OrganizationID: c.Org(), ActorUserID: actor, TargetID: site.ID, TargetName: site.Slug, RequestID: uuid.NewString(), Version: site.Version, Ref: key, Revision: contract.Revision, IntervalSeconds: timeout, ExpectedSource: nvkeParameterDigest(parameters)}
		if err := validateNVKERecovery(saved); err != nil {
			return err
		}
		if err := createReviewedRequest(filename, *saved); err != nil {
			return errors.New("NVKE recovery identity could not be saved before dispatch; preserve any partial file")
		}
	}
	row, err := c.RequestNVKEOperation(ctx, api.NVKERequestInput{SiteID: saved.TargetID, ExpectedSiteVersion: saved.Version, RequestID: saved.RequestID, OperationKey: saved.Ref, Parameters: json.RawMessage(parameters), TimeoutSeconds: saved.IntervalSeconds})
	if err != nil {
		return fmt.Errorf("%w; preserve --request-file and the original input to recover this request", err)
	}
	if nvkeGUID(row.ID) != nil || row.SiteID != saved.TargetID || row.RequestID != saved.RequestID || row.OperationKey != saved.Ref {
		return errors.New("NVKE operation reply did not confirm the original identity; preserve the recovery file")
	}
	if boolFlag(cmd, "wait") {
		return waitNVKEOperation(cmd, ctx, c, row)
	}
	return nvkePrintOperation(cmd, row)
}
