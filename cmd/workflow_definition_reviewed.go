package cmd

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

type reviewedDefinitionField struct {
	Name        string          `json:"name"`
	Kind        string          `json:"kind"`
	Required    bool            `json:"required"`
	HasDefault  bool            `json:"hasDefault"`
	Default     json.RawMessage `json:"default"`
	Sensitive   bool            `json:"sensitive"`
	Simple      bool            `json:"simple"`
	EnumValues  json.RawMessage `json:"enumValues"`
	Constraints json.RawMessage `json:"constraints"`
}

type reviewedDefinition struct {
	GUID          string `json:"guid"`
	Revision      string `json:"revision"`
	InputContract struct {
		Schema             json.RawMessage           `json:"schema"`
		Digest             string                    `json:"digest"`
		Supported          bool                      `json:"supported"`
		AcceptsInputs      bool                      `json:"acceptsInputs"`
		SupportsSimpleForm bool                      `json:"supportsSimpleForm"`
		Fields             []reviewedDefinitionField `json:"fields"`
	} `json:"inputContract"`
	Definition struct {
		GUID             string  `json:"guid"`
		Name             string  `json:"name"`
		Slug             string  `json:"slug"`
		IsEnabled        bool    `json:"isEnabled"`
		IsGlobal         bool    `json:"isGlobal"`
		OrganizationGUID *string `json:"organizationGuid"`
	} `json:"definition"`
}

type reviewedDefinitionStart struct {
	ID                 string  `json:"id"`
	RequestID          string  `json:"requestId"`
	DefinitionID       string  `json:"definitionId"`
	OrganizationID     string  `json:"organizationId"`
	DefinitionRevision string  `json:"definitionRevision"`
	InputSchemaDigest  string  `json:"inputSchemaDigest"`
	ExecutionID        string  `json:"executionId"`
	TemporalWorkflowID string  `json:"temporalWorkflowId"`
	TemporalRunID      *string `json:"temporalRunId"`
	DispatchStatus     string  `json:"dispatchStatus"`
}

const reviewedDefinitionQuery = `query ReviewedWorkflowDefinition($id: GUID!) { workflowDefinitionById(id: $id) { guid revision inputContract { schema digest supported acceptsInputs supportsSimpleForm fields { name kind required hasDefault default sensitive simple enumValues constraints } } definition { guid name slug isEnabled isGlobal organizationGuid } } }`
const reviewedDefinitionStartFields = `id requestId definitionId organizationId definitionRevision inputSchemaDigest executionId temporalWorkflowId temporalRunId dispatchStatus`
const reviewedDefinitionRecoveryQuery = `query WorkflowDefinitionStartRequest($requestId: String!) { workflowDefinitionStartRequest(requestId: $requestId) { ` + reviewedDefinitionStartFields + ` } }`
const reviewedDefinitionStartMutation = `mutation StartWorkflowDefinition($input: StartWorkflowDefinitionInput!) { startWorkflowDefinition(input: $input) { ok errors { code } data { ` + reviewedDefinitionStartFields + ` } } }`

func definitionHashValid(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func definitionGUIDValid(s string) bool {
	u, err := uuid.Parse(s)
	return err == nil && u != uuid.Nil && u.String() == s
}

// Input validation belongs to the server's reviewed JSON Schema 2020-12
// contract. The CLI bounds/parses the object without coercing JSON numbers.
func readDefinitionInputs(filename string) (map[string]interface{}, error) {
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
		return nil, errors.New("inputs must be a readable regular JSON file of at most 64 KiB")
	}
	f, err := os.Open(filename)
	if err != nil {
		return nil, errors.New("cannot read the inputs file")
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Size() > 65536 {
		return nil, errors.New("inputs file changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, errors.New("cannot read a bounded inputs file")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var inputs map[string]interface{}
	if err := d.Decode(&inputs); err != nil || inputs == nil {
		return nil, errors.New("inputs must contain one JSON object")
	}
	var extra interface{}
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("inputs must contain one JSON object")
	}
	return inputs, nil
}

func fetchReviewedDefinition(ctx context.Context, client *api.Client, id string) (*reviewedDefinition, error) {
	if !definitionGUIDValid(id) {
		return nil, errors.New("an exact canonical definition GUID is required")
	}
	var resp struct {
		Definition *reviewedDefinition `json:"workflowDefinitionById"`
	}
	if err := client.GraphQL(ctx, reviewedDefinitionQuery, map[string]interface{}{"id": id}, &resp); err != nil {
		return nil, errors.New("cannot review the exact definition; check current API support and read permission")
	}
	r := resp.Definition
	if r == nil || r.GUID != id || r.Definition.GUID != id || !definitionHashValid(r.Revision) || !definitionHashValid(r.InputContract.Digest) {
		return nil, errors.New("the exact definition or its revision/schema identity is unavailable")
	}
	if (r.Definition.IsGlobal && r.Definition.OrganizationGUID != nil) || (!r.Definition.IsGlobal && (r.Definition.OrganizationGUID == nil || *r.Definition.OrganizationGUID != client.Org())) {
		return nil, errors.New("definition organization identity is unverified")
	}
	return r, nil
}

func runDefinitionReview(cmd *cobra.Command, ctx context.Context, client *api.Client, id string) error {
	r, err := fetchReviewedDefinition(ctx, client, id)
	if err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, r)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Definition %s (%s)\nRevision: %s\nInput schema: %s\nEnabled: %t; supported: %t; accepts inputs: %t; simple form: %t\n", r.GUID, r.Definition.Name, r.Revision, r.InputContract.Digest, r.Definition.IsEnabled, r.InputContract.Supported, r.InputContract.AcceptsInputs, r.InputContract.SupportsSimpleForm)
	b, err := json.MarshalIndent(r.InputContract, "", "  ")
	if err != nil {
		return errors.New("cannot render the input contract")
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(b))
	return nil
}

func checkDefinitionRequest(r *reviewedStartRequest, server, org string, actor int) error {
	if err := r.checkScope("workflow-definition", server, org, actor); err != nil {
		return err
	}
	if !definitionGUIDValid(r.TargetID) || !definitionGUIDValid(r.RequestID) || !definitionHashValid(r.Revision) || !definitionHashValid(r.InputSchemaDigest) || r.Version != 0 || r.Ref != "" {
		return errors.New("request file has invalid definition review metadata")
	}
	return nil
}

func checkDefinitionStart(r *reviewedDefinitionStart, request *reviewedStartRequest) error {
	if r == nil || !definitionGUIDValid(r.ID) || !definitionGUIDValid(r.ExecutionID) || r.RequestID != request.RequestID || r.DefinitionID != request.TargetID || r.OrganizationID != request.OrganizationID || r.DefinitionRevision != request.Revision || r.InputSchemaDigest != request.InputSchemaDigest || r.TemporalWorkflowID == "" {
		return errors.New("response does not establish the original definition, request, revision, schema and execution identity")
	}
	switch r.DispatchStatus {
	case "reserved", "uncertain", "submitted", "refused":
		return nil
	default:
		return errors.New("response has an unrecognized dispatch state")
	}
}

func readDefinitionStart(ctx context.Context, client *api.Client, request *reviewedStartRequest) (*reviewedDefinitionStart, error) {
	var resp struct {
		Start *reviewedDefinitionStart `json:"workflowDefinitionStartRequest"`
	}
	if err := client.GraphQL(ctx, reviewedDefinitionRecoveryQuery, map[string]interface{}{"requestId": request.RequestID}, &resp); err != nil {
		return nil, errors.New("cannot reconcile the original request; no start was submitted, retain the request file")
	}
	if resp.Start != nil {
		if err := checkDefinitionStart(resp.Start, request); err != nil {
			return nil, err
		}
	}
	return resp.Start, nil
}

func printDefinitionStart(cmd *cobra.Command, r *reviewedDefinitionStart, filename string, requireSubmitted bool) error {
	if boolFlag(cmd, "json") {
		if err := renderJSON(cmd, map[string]interface{}{"start": r, "requestFile": filename}); err != nil {
			return err
		}
	} else if r != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Execution %s; dispatch %s\nEngine workflow: %s\n", r.ExecutionID, r.DispatchStatus, r.TemporalWorkflowID)
		if r.TemporalRunID != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Engine run: %s\n", *r.TemporalRunID)
		}
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "Original request outcome is unknown; no visible start record was returned.")
	}
	if r == nil {
		return errors.New("original request outcome is unknown; retain its key and reconcile, never resubmit inputs or create a replacement key")
	}
	if requireSubmitted && (r.DispatchStatus != "submitted" || r.TemporalRunID == nil || *r.TemporalRunID == "") {
		return errors.New("engine submission is unconfirmed; retain the request file and reconcile without resubmitting inputs")
	}
	return nil
}

func runDefinitionStart(cmd *cobra.Command, ctx context.Context, client *api.Client, id string) error {
	filename, _ := cmd.Flags().GetString("request-file")
	if filename == "" {
		return errors.New("--request-file is required to preserve the original request identity")
	}
	if !definitionGUIDValid(id) {
		return errors.New("an exact canonical definition GUID is required")
	}
	server, actor, err := reviewedRequestScope(cmd, client)
	if err != nil {
		return errors.New("cannot verify the current server, organization and actor")
	}
	r, err := readReviewedRequest(filename)
	if err == nil {
		if err := checkDefinitionRequest(r, server, client.Org(), actor); err != nil {
			return err
		}
		if r.TargetID != id {
			return errors.New("definition GUID differs from the saved request")
		}
		// Existing files are read-only recovery even when --yes or inputs flags
		// are present. Never open inputs or inspect the current definition here.
		start, err := readDefinitionStart(ctx, client, r)
		if err != nil {
			return err
		}
		return printDefinitionStart(cmd, start, filename, true)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot read a valid private request file; it has not been replaced")
	}
	if !boolFlag(cmd, "yes") {
		return errors.New("--yes is required to dispatch a new reviewed definition")
	}
	definition, err := fetchReviewedDefinition(ctx, client, id)
	if err != nil {
		return err
	}
	if !definition.Definition.IsEnabled || !definition.InputContract.Supported {
		return errors.New("the definition is disabled or its input schema is unsupported; no start was submitted")
	}
	expectedRevision, _ := cmd.Flags().GetString("expected-revision")
	expectedDigest, _ := cmd.Flags().GetString("expected-input-schema-digest")
	if (expectedRevision == "") != (expectedDigest == "") {
		return errors.New("provide both expected revision and input schema digest, or neither")
	}
	if expectedRevision != "" && (expectedRevision != definition.Revision || expectedDigest != definition.InputContract.Digest) {
		return errors.New("definition revision or input schema changed since review; no start was submitted")
	}
	inputsFilename, _ := cmd.Flags().GetString("inputs-file")
	inputs := map[string]interface{}{}
	if definition.InputContract.AcceptsInputs {
		if inputsFilename == "" {
			return errors.New("--inputs-file is required for this input contract; use a JSON object, including {} for defaults")
		}
		inputs, err = readDefinitionInputs(inputsFilename)
		if err != nil {
			return err
		}
	} else if inputsFilename != "" {
		return errors.New("this definition accepts no inputs; omit --inputs-file")
	}
	r = &reviewedStartRequest{Format: 1, Kind: "workflow-definition", Server: server, OrganizationID: client.Org(), ActorUserID: actor, TargetID: id, TargetName: definition.Definition.Name, RequestID: uuid.NewString(), Revision: definition.Revision, InputSchemaDigest: definition.InputContract.Digest}
	if err := createReviewedRequest(filename, *r); err != nil {
		return errors.New("cannot durably save the request identity; no start was submitted, retain any created file")
	}
	var resp struct {
		Result struct {
			OK    bool                     `json:"ok"`
			Start *reviewedDefinitionStart `json:"data"`
		} `json:"startWorkflowDefinition"`
	}
	input := map[string]interface{}{"definitionId": id, "expectedRevision": r.Revision, "expectedInputSchemaDigest": r.InputSchemaDigest, "requestId": r.RequestID, "inputs": inputs, "confirmed": true}
	if err := client.GraphQL(ctx, reviewedDefinitionStartMutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return errors.New("start outcome is unknown; retain the request file and use workflow definition-reconcile, without resubmitting inputs")
	}
	if resp.Result.Start != nil {
		if err := checkDefinitionStart(resp.Result.Start, r); err != nil {
			return err
		}
		if err := printDefinitionStart(cmd, resp.Result.Start, filename, true); err != nil {
			return err
		}
	}
	if !resp.Result.OK {
		return errors.New("start was not confirmed; retain the request file and reconcile its original identity")
	}
	if resp.Result.Start == nil {
		return errors.New("start returned no verified execution identity; retain the request file and reconcile")
	}
	return nil
}

func runDefinitionReconcile(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	filename, _ := cmd.Flags().GetString("request-file")
	if filename == "" {
		return errors.New("--request-file is required")
	}
	r, err := readReviewedRequest(filename)
	if err != nil {
		return errors.New("cannot read a valid private request file")
	}
	server, actor, err := reviewedRequestScope(cmd, client)
	if err != nil {
		return errors.New("cannot verify the current server, organization and actor")
	}
	if err := checkDefinitionRequest(r, server, client.Org(), actor); err != nil {
		return err
	}
	start, err := readDefinitionStart(ctx, client, r)
	if err != nil {
		return err
	}
	return printDefinitionStart(cmd, start, filename, false)
}

var workflowDefinitionReviewCmd = &cobra.Command{
	Use: "definition-review <definition-guid>", Short: "Review an exact definition revision and JSON input contract", Args: cobra.ExactArgs(1),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runDefinitionReview(cmd, ctx, client, args[0])
	}),
}
var workflowDefinitionStartCmd = &cobra.Command{
	Use: "definition-start <definition-guid>", Short: "Start a reviewed definition with a durable metadata-only request file", Args: cobra.ExactArgs(1),
	Long: "Start an exact definition GUID after reviewing its current revision and input contract. An existing request file only reconciles the original request; it never reads or resubmits inputs. Engine submission does not mean execution completion.",
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runDefinitionStart(cmd, ctx, client, args[0])
	}),
}
var workflowDefinitionReconcileCmd = &cobra.Command{
	Use: "definition-reconcile", Short: "Read the original definition start without submitting inputs", Args: cobra.NoArgs,
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runDefinitionReconcile(cmd, ctx, client)
	}),
}

func init() {
	workflowCmd.AddCommand(workflowDefinitionReviewCmd, workflowDefinitionStartCmd, workflowDefinitionReconcileCmd)
	workflowDefinitionStartCmd.Flags().String("request-file", "", "Private metadata recovery file (required; created before dispatch)")
	workflowDefinitionStartCmd.Flags().String("inputs-file", "", "Readable JSON object file, at most 64 KiB; never saved in the recovery file")
	workflowDefinitionStartCmd.Flags().String("expected-revision", "", "Revision from a prior review (requires expected input schema digest)")
	workflowDefinitionStartCmd.Flags().String("expected-input-schema-digest", "", "Schema digest from a prior review (requires expected revision)")
	workflowDefinitionStartCmd.Flags().Bool("yes", false, "Confirm dispatch of a new reviewed request")
	workflowDefinitionReconcileCmd.Flags().String("request-file", "", "Original private metadata recovery file (required)")
}
