package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

type reviewedPipeline struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Version        int    `json:"version"`
	OrganizationID string `json:"organizationId"`
	DefaultBranch  string `json:"defaultBranch"`
	RepoURL        string `json:"repoUrl"`
	TomlPath       string `json:"tomlPath"`
	CreatedAt      string `json:"createdAt"`
}

type pipelineCursorPage[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"nextCursor"`
	TotalCount *int    `json:"totalCount"`
}

func pipelinePageVariables(cmd *cobra.Command, limit int) (map[string]interface{}, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("--limit must be between 1 and 100")
	}
	after, _ := cmd.Flags().GetString("after")
	search, _ := cmd.Flags().GetString("search")
	var cursor *string
	if after != "" {
		cursor = &after
	}
	return map[string]interface{}{"limit": limit, "after": cursor, "search": search}, nil
}

func listReviewedPipelines(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	vars, err := pipelinePageVariables(cmd, pipelineListLimit)
	if err != nil {
		return err
	}
	var resp struct {
		Page *pipelineCursorPage[reviewedPipeline] `json:"astroliftPipelinesPage"`
	}
	query := `query ListPipelines($limit: Int!, $after: String, $search: String) { astroliftPipelinesPage(limit: $limit, after: $after, search: $search) { items { id name repoUrl defaultBranch tomlPath createdAt version organizationId } nextCursor totalCount } }`
	if err := client.GraphQL(ctx, query, vars, &resp); err != nil {
		return fmt.Errorf("listing pipelines: %w", err)
	}
	if resp.Page == nil {
		return errors.New("pipeline page was not returned")
	}
	for _, p := range resp.Page.Items {
		if p.OrganizationID != client.Org() || p.Version < 1 {
			return errors.New("pipeline page returned unverified organization/version")
		}
		if _, err := uuid.Parse(p.ID); err != nil {
			return errors.New("pipeline page returned invalid GUID")
		}
	}
	if pipelineListJSON || boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Page)
	}
	for _, p := range resp.Page.Items {
		fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  branch: %s\n", p.ID, p.Name, p.DefaultBranch)
	}
	if len(resp.Page.Items) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No pipelines on this page.")
	}
	if resp.Page.NextCursor != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "More pipelines: pass --after %s\n", *resp.Page.NextCursor)
	}
	return nil
}

func listReviewedPipelineRuns(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	vars, err := pipelinePageVariables(cmd, pipelineRunsLimit)
	if err != nil {
		return err
	}
	id, err := resolvePipelineID(ctx, client, pipelineRunsPipeline)
	if err != nil {
		return err
	}
	vars["pipelineId"] = id
	var resp struct {
		Page *pipelineCursorPage[reviewedPipelineRun] `json:"astroliftPipelineRunsPage"`
	}
	query := `query ListPipelineRuns($pipelineId: String!, $limit: Int!, $after: String, $search: String) { astroliftPipelineRunsPage(pipelineId: $pipelineId, limit: $limit, after: $after, search: $search) { items { ` + reviewedPipelineRunFields + ` } nextCursor totalCount } }`
	if err := client.GraphQL(ctx, query, vars, &resp); err != nil {
		return fmt.Errorf("listing runs: %w", err)
	}
	if resp.Page == nil {
		return errors.New("pipeline runs page was not returned")
	}
	for i := range resp.Page.Items {
		if err := checkPipelineRun(&resp.Page.Items[i], client.Org(), id); err != nil {
			return err
		}
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Page)
	}
	for i := range resp.Page.Items {
		if err := printReviewedPipelineRun(cmd, &resp.Page.Items[i], ""); err != nil {
			return err
		}
	}
	if len(resp.Page.Items) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No runs on this page.")
	}
	if resp.Page.NextCursor != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "More runs: pass --after %s\n", *resp.Page.NextCursor)
	}
	return nil
}

type reviewedPipelineRun struct {
	ID                     string  `json:"id"`
	RunNumber              int     `json:"runNumber"`
	Version                int     `json:"version"`
	PipelineID             string  `json:"pipelineId"`
	OrganizationID         string  `json:"organizationId"`
	PipelineVersion        int     `json:"pipelineVersion"`
	RequestID              string  `json:"requestId"`
	TriggerRef             string  `json:"triggerRef"`
	Status                 string  `json:"status"`
	TemporalWorkflowID     string  `json:"temporalWorkflowId"`
	TemporalRunID          string  `json:"temporalRunId"`
	DispatchStatus         string  `json:"dispatchStatus"`
	CancellationStatus     string  `json:"cancellationStatus"`
	CancellationObservedAt *string `json:"cancellationObservedAt"`
	CleanupStatus          string  `json:"cleanupStatus"`
}

// No job results, inputs or error bodies are needed to recover an exact start.
const reviewedPipelineRunFields = `id runNumber version pipelineId organizationId pipelineVersion requestId triggerRef status temporalWorkflowId temporalRunId dispatchStatus cancellationStatus cancellationObservedAt cleanupStatus`

func exactPipeline(ctx context.Context, client *api.Client, id string) (*reviewedPipeline, error) {
	var resp struct {
		Pipeline *reviewedPipeline `json:"astroliftPipeline"`
	}
	if err := client.GraphQL(ctx, `query ReviewedPipeline($id: String!) { astroliftPipeline(id: $id) { id name version organizationId defaultBranch } }`, map[string]interface{}{"id": id}, &resp); err != nil {
		return nil, fmt.Errorf("reviewing pipeline: %w", err)
	}
	p := resp.Pipeline
	if p == nil || p.ID != id || p.OrganizationID != client.Org() || p.Version < 1 {
		return nil, errors.New("the exact pipeline is unavailable or its organization/version is unverified")
	}
	return p, nil
}

func resolveReviewedPipeline(ctx context.Context, client *api.Client, nameOrID string) (*reviewedPipeline, error) {
	id, err := resolvePipelineID(ctx, client, nameOrID)
	if err != nil {
		return nil, err
	}
	return exactPipeline(ctx, client, id)
}

func checkPipelineRun(r *reviewedPipelineRun, org, pipelineID string) error {
	if r == nil || r.ID == "" || r.OrganizationID != org || r.PipelineID != pipelineID || r.Version < 1 || r.PipelineVersion < 1 || r.Status == "" || r.DispatchStatus == "" || r.CancellationStatus == "" || r.CleanupStatus == "" || r.TemporalWorkflowID != "pipeline-run-"+r.ID {
		return errors.New("run response does not establish the exact pipeline, organization, versions and reserved engine identity")
	}
	if _, err := uuid.Parse(r.ID); err != nil {
		return errors.New("run response has an invalid GUID")
	}
	return nil
}

func checkPipelineStart(r *reviewedPipelineRun, request *reviewedStartRequest) error {
	if err := checkPipelineRun(r, request.OrganizationID, request.TargetID); err != nil {
		return err
	}
	if r.RequestID != request.RequestID || r.PipelineVersion != request.Version || r.TriggerRef != request.Ref {
		return errors.New("run response belongs to another request, reviewed version or branch")
	}
	return nil
}

func readPipelineStart(ctx context.Context, client *api.Client, r *reviewedStartRequest) (*reviewedPipelineRun, error) {
	var resp struct {
		Run *reviewedPipelineRun `json:"pipelineStartRequest"`
	}
	query := `query PipelineStartRequest($pipelineId: GUID!, $requestId: String!) { pipelineStartRequest(pipelineId: $pipelineId, requestId: $requestId) { ` + reviewedPipelineRunFields + ` } }`
	if err := client.GraphQL(ctx, query, map[string]interface{}{"pipelineId": r.TargetID, "requestId": r.RequestID}, &resp); err != nil {
		return nil, fmt.Errorf("reconciling the original request (no start submitted): %w", err)
	}
	if resp.Run != nil {
		if err := checkPipelineStart(resp.Run, r); err != nil {
			return nil, err
		}
	}
	return resp.Run, nil
}

func printReviewedPipelineRun(cmd *cobra.Command, r *reviewedPipelineRun, requestFile string) error {
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, map[string]interface{}{"run": r, "requestFile": requestFile})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Run #%d (%s): %s; dispatch %s; cancellation %s; cleanup %s\n", r.RunNumber, r.ID, r.Status, r.DispatchStatus, r.CancellationStatus, r.CleanupStatus)
	if requestFile != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Recovery request: %s\n", requestFile)
	}
	return nil
}

func printPipelineStartOutcome(cmd *cobra.Command, r *reviewedPipelineRun, filename string) error {
	if err := printReviewedPipelineRun(cmd, r, filename); err != nil {
		return err
	}
	if r.DispatchStatus != "submitted" || r.TemporalRunID == "" {
		return errors.New("run is reserved but engine submission is unconfirmed; reconcile the saved request, without creating a new key")
	}
	return nil
}

func reviewedPipelineStart(cmd *cobra.Command, ctx context.Context, client *api.Client, selector string) error {
	filename, _ := cmd.Flags().GetString("request-file")
	if filename == "" {
		return errors.New("--request-file is required; retain it to reconcile an uncertain start")
	}
	server, actor, err := reviewedRequestScope(cmd, client)
	if err != nil {
		return err
	}
	r, err := readReviewedRequest(filename)
	if err == nil {
		if err := r.checkScope("pipeline", server, client.Org(), actor); err != nil {
			return err
		}
		if selector != r.TargetID && selector != r.TargetName {
			return errors.New("pipeline selector differs from the saved request")
		}
		if cmd.Flags().Changed("branch") && strings.TrimSpace(pipelineRunBranch) != r.Ref {
			return errors.New("branch differs from the saved request; reconcile its original branch")
		}
		if run, err := readPipelineStart(ctx, client, r); err != nil {
			return err
		} else if run != nil {
			return printPipelineStartOutcome(cmd, run, filename)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !boolFlag(cmd, "yes") {
		return errors.New("--yes is required to dispatch the reviewed pipeline; reconciliation never submits a new start")
	}
	var p *reviewedPipeline
	if r == nil {
		p, err = resolveReviewedPipeline(ctx, client, selector)
	} else {
		p, err = exactPipeline(ctx, client, r.TargetID)
	}
	if err != nil {
		return err
	}
	if r == nil {
		ref := strings.TrimSpace(pipelineRunBranch)
		if ref == "" {
			ref = strings.TrimSpace(p.DefaultBranch)
		}
		r = &reviewedStartRequest{Format: 1, Kind: "pipeline", Server: server, OrganizationID: client.Org(), ActorUserID: actor, TargetID: p.ID, TargetName: p.Name, RequestID: uuid.NewString(), Version: p.Version, Ref: ref}
		if err := createReviewedRequest(filename, *r); err != nil {
			return err
		}
	} else if p.Version != r.Version {
		return errors.New("pipeline changed since this request was reviewed; do not replace the original key while its outcome is uncertain")
	}
	var resp struct {
		Result struct {
			OK     bool `json:"ok"`
			Errors []struct {
				Code string `json:"code"`
			} `json:"errors"`
			Run *reviewedPipelineRun `json:"data"`
		} `json:"startPipelineRun"`
	}
	mutation := `mutation StartPipelineRun($input: StartPipelineRunInput!) { startPipelineRun(input: $input) { ok errors { code } data { ` + reviewedPipelineRunFields + ` } } }`
	input := map[string]interface{}{"pipelineId": r.TargetID, "expectedVersion": r.Version, "requestId": r.RequestID, "ref": r.Ref, "confirmed": true}
	if err := client.GraphQL(ctx, mutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("start outcome is unknown; keep %s and run pipeline reconcile --request-file with this file: %w", filename, err)
	}
	if !resp.Result.OK {
		return fmt.Errorf("start refused (%v); request retained at %s", resp.Result.Errors, filename)
	}
	if err := checkPipelineStart(resp.Result.Run, r); err != nil {
		return err
	}
	return printPipelineStartOutcome(cmd, resp.Result.Run, filename)
}

var pipelineReconcileCmd = &cobra.Command{
	Use: "reconcile", Short: "Read an original pipeline start without resubmitting",
	Args: cobra.NoArgs,
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		filename, _ := cmd.Flags().GetString("request-file")
		r, err := readReviewedRequest(filename)
		if err != nil {
			return err
		}
		server, actor, err := reviewedRequestScope(cmd, client)
		if err != nil {
			return err
		}
		if err := r.checkScope("pipeline", server, client.Org(), actor); err != nil {
			return err
		}
		run, err := readPipelineStart(ctx, client, r)
		if err != nil {
			return err
		}
		if run == nil {
			return errors.New("no visible run was found for this actor's original request; this command has not submitted anything")
		}
		return printReviewedPipelineRun(cmd, run, filename)
	}),
}

func exactPipelineRun(ctx context.Context, client *api.Client, id string) (*reviewedPipelineRun, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, errors.New("an exact run GUID is required")
	}
	var resp struct {
		Run *reviewedPipelineRun `json:"astroliftPipelineRun"`
	}
	if err := client.GraphQL(ctx, `query ReviewedPipelineRun($id: String!) { astroliftPipelineRun(id: $id) { `+reviewedPipelineRunFields+` } }`, map[string]interface{}{"id": id}, &resp); err != nil {
		return nil, err
	}
	if resp.Run == nil || resp.Run.ID != id {
		return nil, errors.New("the exact run is unavailable")
	}
	if err := checkPipelineRun(resp.Run, client.Org(), resp.Run.PipelineID); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(resp.Run.PipelineID); err != nil {
		return nil, errors.New("run pipeline GUID is unverified")
	}
	return resp.Run, nil
}

var pipelineShowCmd = &cobra.Command{Use: "show <run-guid>", Short: "Read exact run, submission, cancellation and cleanup state", Args: cobra.ExactArgs(1), RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
	r, err := exactPipelineRun(ctx, client, args[0])
	if err != nil {
		return err
	}
	return printReviewedPipelineRun(cmd, r, "")
})}

func reviewedPipelineCancel(cmd *cobra.Command, ctx context.Context, client *api.Client, id string) error {
	if !boolFlag(cmd, "yes") {
		return errors.New("--yes is required to request cancellation of the exact reviewed run")
	}
	r, err := exactPipelineRun(ctx, client, id)
	if err != nil {
		return err
	}
	if r.TemporalRunID == "" {
		return errors.New("engine run identity is unconfirmed; reconcile the original start before cancellation")
	}
	var resp struct {
		Result struct {
			OK     bool `json:"ok"`
			Errors []struct {
				Code string `json:"code"`
			} `json:"errors"`
			Run *reviewedPipelineRun `json:"data"`
		} `json:"cancelPipelineRun"`
	}
	query := `mutation CancelPipelineRun($runId: GUID!, $expectedVersion: Int!, $temporalWorkflowId: String!, $temporalRunId: String!, $confirmed: Boolean!) { cancelPipelineRun(runId: $runId, expectedVersion: $expectedVersion, temporalWorkflowId: $temporalWorkflowId, temporalRunId: $temporalRunId, confirmed: $confirmed) { ok errors { code } data { ` + reviewedPipelineRunFields + ` } } }`
	vars := map[string]interface{}{"runId": r.ID, "expectedVersion": r.Version, "temporalWorkflowId": r.TemporalWorkflowID, "temporalRunId": r.TemporalRunID, "confirmed": true}
	if err := client.GraphQL(ctx, query, vars, &resp); err != nil {
		return fmt.Errorf("cancellation outcome is unknown; inspect the same run with pipeline show %s: %w", r.ID, err)
	}
	if !resp.Result.OK {
		return fmt.Errorf("cancellation refused (%v)", resp.Result.Errors)
	}
	result := resp.Result.Run
	if err := checkPipelineRun(result, client.Org(), r.PipelineID); err != nil {
		return err
	}
	if result.ID != r.ID || result.TemporalWorkflowID != r.TemporalWorkflowID || result.TemporalRunID != r.TemporalRunID {
		return errors.New("cancellation response changed the reviewed run or engine identity")
	}
	if err := printReviewedPipelineRun(cmd, result, ""); err != nil {
		return err
	}
	if result.CancellationStatus != "acknowledged" && result.CancellationStatus != "observed" {
		return errors.New("cancellation is unconfirmed; inspect the same exact run before retrying")
	}
	return nil
}

func init() {
	for _, cmd := range []*cobra.Command{pipelineListCmd, pipelineRunsCmd} {
		cmd.Flags().String("after", "", "Continue with the server cursor from the previous page")
		cmd.Flags().String("search", "", "Server-side search within the selected organization")
	}
	pipelineRunCmd.Flags().String("request-file", "", "Private metadata file for this start's persistent request ID (required)")
	pipelineRunCmd.Flags().Bool("yes", false, "Confirm the exact pipeline and current reviewed version")
	pipelineCancelCmd.Flags().Bool("yes", false, "Confirm cancellation of the exact run and engine IDs")
	pipelineReconcileCmd.Flags().String("request-file", "", "Original start metadata file (required)")
	_ = pipelineReconcileCmd.MarkFlagRequired("request-file")
	pipelineCmd.AddCommand(pipelineShowCmd, pipelineReconcileCmd)
}
