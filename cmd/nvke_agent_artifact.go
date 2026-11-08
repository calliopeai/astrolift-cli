package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/spf13/cobra"
)

func nvkeAgentJobFlags(cmd *cobra.Command) {
	nvkeAgentWriteFlags(cmd, false)
	cmd.Flags().Int("version", 0, "Exact reviewed operator operation version (required)")
	_ = cmd.MarkFlagRequired("version")
}
func nvkeAgentJobReview(cmd *cobra.Command, ctx context.Context, c *api.Client, id string) (api.NVKEAgentJobReview, *api.NVKEOperation, error) {
	if nvkeGUID(id) != nil {
		return api.NVKEAgentJobReview{}, nil, errors.New("use an exact operation GUID")
	}
	authority, err := nvkeAgentReview(cmd, c, false)
	if err != nil {
		return api.NVKEAgentJobReview{}, nil, err
	}
	version, _ := cmd.Flags().GetInt("version")
	if version < 1 {
		return api.NVKEAgentJobReview{}, nil, errors.New("use an exact positive operator operation version")
	}
	row, err := c.NVKEOperation(ctx, id)
	if err != nil {
		return api.NVKEAgentJobReview{}, nil, err
	}
	if row.ID != id || row.SiteID != authority.SiteID || row.Version != version || !strings.HasPrefix(row.OperationKey, "tooling.") || len(row.OperationKey) <= 7 || row.IsInventory || row.ResourceSource != nil || row.ProviderSource != nil {
		return api.NVKEAgentJobReview{}, nil, errors.New("reviewed operator operation metadata changed or belongs to another site")
	}
	return api.NVKEAgentJobReview{OperationID: id, ExpectedActorID: authority.ExpectedActorID, ExpectedSiteVersion: authority.ExpectedSiteVersion, ExpectedAuthorityVersion: authority.ExpectedVersion, ExpectedOperationVersion: version}, row, nil
}
func newNVKEAgentReconcileCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "reconcile OPERATION_GUID", Args: cobra.ExactArgs(1), Short: "Read the original uncertain job under reviewed current admin authority; never submit", RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
		review, source, err := nvkeAgentJobReview(cmd, ctx, c, args[0])
		if err != nil {
			return err
		}
		if source.Status != "UNCERTAIN" {
			return errors.New("only an uncertain operator job needs reviewed reconciliation")
		}
		row, err := c.ReconcileNVKEAgentJob(ctx, review)
		if err != nil {
			return err
		}
		if row == nil || row.ID != source.ID || row.SiteID != source.SiteID || row.RequestID != source.RequestID || row.OperationKey != source.OperationKey || row.Version <= source.Version || row.IsInventory || row.ResourceSource != nil || row.ProviderSource != nil {
			return errors.New("operator reconciliation did not confirm the original receipt; inspect operation get")
		}
		return nvkePrintOperation(cmd, row)
	})}
	nvkeAgentJobFlags(cmd)
	return cmd
}
func newNVKEAgentArtifactCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "artifact OPERATION_GUID", Args: cobra.ExactArgs(1), Short: "Download a complete retained artifact privately with source, range and SHA-256 checks", Long: `Save a retained private artifact to a new protected local file. No artifact bytes
are printed. Each chunk uses the reviewed current site, agent and operation versions.
The complete download must match its original size and SHA-256. Failed downloads
retain a protected partial file for inspection; do not use it as a credential.`, RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
		name, output := resourceStringFlag(cmd, "name"), resourceStringFlag(cmd, "output")
		maximum, _ := cmd.Flags().GetInt64("max-bytes")
		if name == "" || len(name) > 4096 || output == "" || output == "-" || maximum < 1 {
			return errors.New("choose a retained artifact name, new private output path and positive maximum size")
		}
		review, _, err := nvkeAgentJobReview(cmd, ctx, c, args[0])
		if err != nil {
			return err
		}
		size, digest, err := downloadNVKEAgentArtifact(ctx, c, api.NVKEAgentArtifactInput{NVKEAgentJobReview: review, Name: name, Offset: "0", Limit: 262144}, output, maximum)
		if err != nil {
			return err
		}
		return nvkeJSON(cmd, struct {
			OperationID string `json:"operationId"`
			Size        string `json:"size"`
			SHA256      string `json:"sha256"`
			Output      string `json:"output"`
		}{args[0], strconv.FormatInt(size, 10), digest, output})
	})}
	nvkeAgentJobFlags(cmd)
	cmd.Flags().String("name", "", "Retained artifact name from the private operation result (required)")
	_ = cmd.MarkFlagRequired("name")
	cmd.Flags().String("output", "", "New protected local file; never stdout or an existing path (required)")
	_ = cmd.MarkFlagRequired("output")
	cmd.Flags().Int64("max-bytes", 64*1024*1024, "Maximum accepted artifact size; increase explicitly for larger diagnostics")
	return cmd
}
func agentDecimal(value string) (int64, error) {
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number < 0 || strconv.FormatInt(number, 10) != value {
		return 0, errors.New("operator artifact byte range invalid")
	}
	return number, nil
}
func downloadNVKEAgentArtifact(ctx context.Context, c *api.Client, input api.NVKEAgentArtifactInput, filename string, maximum int64) (size int64, digest string, result error) {
	file, err := privatefile.CreateExclusive(filename)
	if err != nil {
		return 0, "", errors.New("operator artifact output must be a new private path; existing files are never replaced")
	}
	defer func() {
		if err := file.Close(); result == nil && err != nil {
			result = errors.New("operator artifact file could not be closed")
		}
	}()
	digestHash := sha256.New()
	offset := int64(0)
	size = -1
	for {
		input.Offset = strconv.FormatInt(offset, 10)
		chunk, err := c.NVKEAgentArtifact(ctx, input)
		if err != nil {
			return 0, "", fmt.Errorf("%w; retain the private partial download and original operation", err)
		}
		total, totalErr := agentDecimal(chunk.Size)
		next, nextErr := agentDecimal(chunk.NextOffset)
		bytes, decodeErr := base64.StdEncoding.Strict().DecodeString(chunk.DataBase64)
		if chunk.ID != input.OperationID || chunk.Name != input.Name || chunk.Offset != input.Offset || totalErr != nil || nextErr != nil || decodeErr != nil || !nvkeDigestPattern.MatchString(chunk.SHA256) || total > maximum || len(bytes) > input.Limit || int64(len(bytes)) > total-offset || next != offset+int64(len(bytes)) || (offset < total && len(bytes) == 0) || (size >= 0 && (total != size || digest != chunk.SHA256)) {
			return 0, "", errors.New("operator artifact identity, content or byte range changed; retain the private partial download")
		}
		size, digest = total, chunk.SHA256
		if _, err = file.Write(bytes); err != nil {
			return 0, "", errors.New("operator artifact write failed; retain the private partial download")
		}
		_, _ = digestHash.Write(bytes)
		offset = next
		if offset == size {
			break
		}
	}
	if hex.EncodeToString(digestHash.Sum(nil)) != digest {
		return 0, "", errors.New("operator artifact SHA-256 mismatch; retain the private partial download")
	}
	if file.Sync() != nil {
		return 0, "", errors.New("operator artifact flush failed; retain the private partial download")
	}
	if err = privatefile.SyncParent(filename); err != nil {
		return 0, "", errors.New("operator artifact directory flush failed; retain the private file")
	}
	return size, digest, nil
}
