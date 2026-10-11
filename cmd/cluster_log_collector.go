package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const clusterCollectorCapability = "clusters.reviewed_log_collector_install"
const clusterCollectorFields = `id clusterId requestId expectedVersion expectedSource retentionDays status stage retryable cleanupPending coverage workflowId deadline postLossVerifiedAt activatedAt activatedClusterVersion errorCode errorMessage readerPolicy createdAt updatedAt`
const clusterCollectorReviewFields = `clusterId version source retentionDays supported refusalCode message policy readerPolicy`

type clusterCollectorReview struct {
	ClusterID     string          `json:"clusterId"`
	Version       int             `json:"version"`
	Source        string          `json:"source"`
	RetentionDays int             `json:"retentionDays"`
	Supported     bool            `json:"supported"`
	RefusalCode   string          `json:"refusalCode"`
	Message       string          `json:"message"`
	Policy        json.RawMessage `json:"policy"`
	ReaderPolicy  json.RawMessage `json:"readerPolicy"`
}

type clusterCollectorOperation struct {
	ID                      string          `json:"id"`
	ClusterID               string          `json:"clusterId"`
	RequestID               string          `json:"requestId"`
	ExpectedVersion         int             `json:"expectedVersion"`
	ExpectedSource          string          `json:"expectedSource"`
	RetentionDays           int             `json:"retentionDays"`
	Status                  string          `json:"status"`
	Stage                   string          `json:"stage"`
	Retryable               bool            `json:"retryable"`
	CleanupPending          bool            `json:"cleanupPending"`
	Coverage                string          `json:"coverage"`
	WorkflowID              string          `json:"workflowId"`
	Deadline                string          `json:"deadline"`
	PostLossVerifiedAt      *string         `json:"postLossVerifiedAt"`
	ActivatedAt             *string         `json:"activatedAt"`
	ActivatedClusterVersion *int            `json:"activatedClusterVersion"`
	ErrorCode               string          `json:"errorCode"`
	ErrorMessage            string          `json:"errorMessage"`
	ReaderPolicy            json.RawMessage `json:"readerPolicy"`
	CreatedAt               string          `json:"createdAt"`
	UpdatedAt               string          `json:"updatedAt"`
}

var collectorSourcePattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func collectorExactUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func fetchClusterCollectorReview(ctx context.Context, client *api.Client, selector reviewedClusterSelector, days int) (*clusterCollectorReview, error) {
	if !collectorRetentionSupported(days) {
		return nil, errors.New("--retention-days must be a supported CloudWatch retention period")
	}
	clusterID, err := selector.resolve(ctx, client)
	if err != nil {
		return nil, err
	}
	var response struct {
		Data *clusterCollectorReview `json:"astroliftClusterLogCollectorReview"`
	}
	query := `query ClusterLogCollectorReview($clusterId: GUID!, $retentionDays: Int!) { astroliftClusterLogCollectorReview(clusterId: $clusterId, retentionDays: $retentionDays) { ` + clusterCollectorReviewFields + ` } }`
	if client.GraphQL(ctx, query, map[string]interface{}{"clusterId": clusterID, "retentionDays": days}, &response) != nil {
		return nil, errors.New("exact collector review is unavailable; no installation was submitted")
	}
	review := response.Data
	if review == nil || review.ClusterID != clusterID || review.Version < 1 || review.RetentionDays != days || (review.Supported && !collectorSourcePattern.MatchString(review.Source)) {
		return nil, errors.New("server returned an unavailable or mismatched collector review")
	}
	return review, nil
}

func runClusterCollectorReview(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string, days int) error {
	selector, err := parseReviewedClusterSelector(cmd, slug)
	if err != nil {
		return err
	}
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := requireClusterInstallCapability(requestCtx, client, clusterCollectorCapability); err != nil {
		return err
	}
	review, err := fetchClusterCollectorReview(requestCtx, client, selector, days)
	if err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		if err := renderJSON(cmd, review); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Collector review for %s: supported=%t, retention=%d days. Candidate reader policy is unattached.\n", review.ClusterID, review.Supported, review.RetentionDays)
		if review.RefusalCode != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Refusal: %s\n", review.RefusalCode)
		}
	}
	if !review.Supported {
		return errors.New("collector preparation is unsupported for this reviewed target; no installation was submitted")
	}
	return nil
}

func runClusterInstallLogCollector(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string, days int) error {
	selector, err := parseReviewedClusterSelector(cmd, slug)
	if err != nil {
		return err
	}
	filename, _ := cmd.Flags().GetString("request-file")
	if strings.TrimSpace(filename) == "" {
		return errors.New("--request-file is required to retain the original collector operation")
	}
	if !collectorRetentionSupported(days) {
		return errors.New("--retention-days must be a supported CloudWatch retention period")
	}
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := requireClusterInstallCapability(requestCtx, client, clusterCollectorCapability); err != nil {
		return err
	}
	server, actor, err := reviewedRequestScope(cmd, client)
	if err != nil {
		return errors.New("current authenticated server, organization and actor are unavailable; preserve any original request file")
	}
	request, err := readCollectorInstallRequest(filename)
	if err == nil {
		if err := request.checkRecoverySelector(server, client.Org(), actor, selector, days, cmd.Flags().Changed("retention-days")); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else {
		review, err := fetchClusterCollectorReview(requestCtx, client, selector, days)
		if err != nil {
			return err
		}
		if !review.Supported {
			return errors.New("collector preparation is unsupported for this reviewed target; no installation was submitted")
		}
		request = &collectorInstallRequest{reviewedStartRequest: reviewedStartRequest{
			Format: 1, Kind: collectorRequestKind, Server: server, OrganizationID: client.Org(), ActorUserID: actor,
			TargetID: review.ClusterID, TargetName: selector.targetName(), RequestID: uuid.NewString(), Version: review.Version, ExpectedSource: review.Source,
		}, RetentionDays: days}
		if err := createCollectorInstallRequest(filename, *request); err != nil {
			return err
		}
	}
	input := map[string]interface{}{"clusterId": request.TargetID, "requestId": request.RequestID, "expectedVersion": request.Version, "expectedSource": request.ExpectedSource, "retentionDays": request.RetentionDays}
	var response struct {
		Result struct {
			noneMutationResult
			Data *clusterCollectorOperation `json:"data"`
		} `json:"astroliftInstallClusterLogCollector"`
	}
	mutation := `mutation InstallClusterLogCollector($input: InstallClusterLogCollectorInput!) { astroliftInstallClusterLogCollector(input: $input) { ok errors { code message field } data { ` + clusterCollectorFields + ` } } }`
	if client.GraphQL(requestCtx, mutation, map[string]interface{}{"input": input}, &response) != nil {
		return errors.New("collector installation outcome is unknown; preserve the original request file and retry the same command and scope")
	}
	if !response.Result.Ok {
		return errors.New("collector request refused; preserve the original request file and use the original scope for recovery")
	}
	if err := checkClusterCollectorOperation(response.Result.Data); err != nil {
		return err
	}
	op := response.Result.Data
	if op.ClusterID != request.TargetID || op.RequestID != request.RequestID || op.ExpectedVersion != request.Version || op.ExpectedSource != request.ExpectedSource || op.RetentionDays != request.RetentionDays {
		return errors.New("server returned a different collector request; preserve the original request file")
	}
	return printClusterCollectorOperation(cmd, op, false)
}

func checkClusterCollectorOperation(op *clusterCollectorOperation) error {
	if op == nil {
		return errors.New("the exact collector operation is unavailable")
	}
	for _, id := range []string{op.ID, op.ClusterID, op.RequestID} {
		if !collectorExactUUID(id) {
			return errors.New("server returned an invalid collector operation identity")
		}
	}
	if op.ExpectedVersion < 1 || !collectorSourcePattern.MatchString(op.ExpectedSource) || !collectorRetentionSupported(op.RetentionDays) {
		return errors.New("server returned an invalid collector request tuple")
	}
	switch op.Status {
	case "QUEUED", "PREPARING", "INSTALLING", "READINESS_PENDING", "READER_GRANT_PENDING", "INGESTION_PENDING", "PROBE_DELETION_PENDING", "POST_LOSS_READ_PENDING", "UNCERTAIN", "REFUSED", "ACTIVATED":
	default:
		return errors.New("server returned an unknown collector operation status")
	}
	if op.Status == "ACTIVATED" {
		if op.PostLossVerifiedAt == nil || op.ActivatedAt == nil || op.ActivatedClusterVersion == nil || *op.ActivatedClusterVersion < 1 || op.CleanupPending || op.Retryable {
			return errors.New("server returned inconsistent collector activation proof")
		}
		for _, value := range []string{*op.PostLossVerifiedAt, *op.ActivatedAt} {
			if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
				return errors.New("server returned invalid collector activation timestamps")
			}
		}
	} else if op.ActivatedAt != nil || op.PostLossVerifiedAt != nil || op.ActivatedClusterVersion != nil {
		return errors.New("server returned activation proof for a non-activated collector operation")
	}
	return nil
}

func printClusterCollectorOperation(cmd *cobra.Command, op *clusterCollectorOperation, readOnly bool) error {
	if boolFlag(cmd, "json") {
		if err := renderJSON(cmd, op); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Collector operation %s: %s (coverage: %s; cleanup pending: %t).\n", op.ID, op.Status, op.Coverage, op.CleanupPending)
		if op.Status == "ACTIVATED" {
			fmt.Fprintln(cmd.OutOrStdout(), "Post-loss read verified and reader activated; this receipt does not establish ongoing collector health.")
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "Installation is not yet activated. Preserve the original request file.")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Read status: astro operator cluster log-collector-status --operation-id %s\n", op.ID)
		if op.ErrorCode != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Operation note: %s\n", op.ErrorCode)
		}
	}
	if !readOnly && (op.Status == "REFUSED" || op.Status == "UNCERTAIN") {
		return errors.New("collector installation is refused or unconfirmed; preserve the original request file")
	}
	return nil
}

func runClusterLogCollectorStatus(cmd *cobra.Command, ctx context.Context, client *api.Client, id string) error {
	clusterID, err := optionalReviewedClusterID(cmd)
	if err != nil {
		return err
	}
	if !collectorExactUUID(id) {
		return errors.New("--operation-id must be a canonical nonzero UUID")
	}
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := requireClusterInstallCapability(readCtx, client, clusterCollectorCapability); err != nil {
		return err
	}
	var response struct {
		Data *clusterCollectorOperation `json:"astroliftClusterLogCollectorOperation"`
	}
	query := `query ClusterLogCollectorStatus($operationId: GUID!) { astroliftClusterLogCollectorOperation(operationId: $operationId) { ` + clusterCollectorFields + ` } }`
	if client.GraphQL(readCtx, query, map[string]interface{}{"operationId": id}, &response) != nil {
		return errors.New("exact collector operation is unavailable to the current actor and credential")
	}
	if err := checkClusterCollectorOperation(response.Data); err != nil {
		return err
	}
	if response.Data.ID != id {
		return errors.New("server returned another collector operation")
	}
	if clusterID != "" && response.Data.ClusterID != clusterID {
		return errors.New("collector operation belongs to another cluster than --cluster-id")
	}
	return printClusterCollectorOperation(cmd, response.Data, true)
}

func collectorClient(cmd *cobra.Command) (*api.Client, error) {
	client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
	return client, err
}

var clusterInstallLogCollectorCmd = &cobra.Command{
	Use: "install-log-collector", Short: "Install and verify the reviewed CloudWatch collector through the server",
	Long: `Install only the server-pinned CloudWatch collector on supported Linux EC2 workers.
The original private request file is durable before dispatch. Retry the same file,
selector and scope after a lost reply; no chart, image or role overrides are accepted.
Pending reader grants need an operator grant; the workflow never attaches external
reader policies. Only ACTIVATED confirms post-pod-loss verification and reader
activation, not ongoing health or tracing. Requires cluster.manage.
Use exactly one of --cluster-id or --slug. Known GUIDs skip inventory discovery;
slug discovery additionally requires cluster.register. Shared platform clusters
also require the platform-operator gate; a known GUID grants no authority.`,
	Args: reviewedClusterSelectorArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		client, err := collectorClient(cmd)
		if err != nil {
			return err
		}
		slug, _ := cmd.Flags().GetString("slug")
		days, _ := cmd.Flags().GetInt("retention-days")
		return runClusterInstallLogCollector(cmd, cmd.Context(), client, slug, days)
	},
}
var clusterLogCollectorReviewCmd = &cobra.Command{
	Use: "log-collector-review", Short: "Read exact collector support and an unattached candidate reader policy",
	Long: `Review a known cluster GUID with --cluster-id, requiring scoped cluster.manage.
Alternatively use --slug with cluster.register inventory access. Choose exactly one
selector. Shared platform clusters also require the platform-operator gate.
Review grants no permission and performs no installation or reader grant.`,
	Args: reviewedClusterSelectorArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		client, err := collectorClient(cmd)
		if err != nil {
			return err
		}
		slug, _ := cmd.Flags().GetString("slug")
		days, _ := cmd.Flags().GetInt("retention-days")
		return runClusterCollectorReview(cmd, cmd.Context(), client, slug, days)
	},
}
var clusterLogCollectorStatusCmd = &cobra.Command{
	Use: "log-collector-status", Short: "Read an exact original collector operation and activation receipt", Args: reviewedClusterStatusArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		client, err := collectorClient(cmd)
		if err != nil {
			return err
		}
		id, _ := cmd.Flags().GetString("operation-id")
		return runClusterLogCollectorStatus(cmd, cmd.Context(), client, id)
	},
}

func init() {
	for _, command := range []*cobra.Command{clusterInstallLogCollectorCmd, clusterLogCollectorReviewCmd} {
		command.Flags().String("slug", "", "Exact cluster slug; discovery requires cluster.register inventory access")
		addReviewedClusterSelectorFlags(command)
		command.Flags().Int("retention-days", 30, "Supported CloudWatch retention period")
	}
	clusterInstallLogCollectorCmd.Flags().String("request-file", "", "Private original collector operation metadata file (required)")
	_ = clusterInstallLogCollectorCmd.MarkFlagRequired("request-file")
	clusterLogCollectorStatusCmd.Flags().String("operation-id", "", "Exact original collector operation UUID (required)")
	clusterLogCollectorStatusCmd.Flags().String("cluster-id", "", "Optional canonical cluster UUID; refuse a status receipt for another target")
	_ = clusterLogCollectorStatusCmd.MarkFlagRequired("operation-id")
	operatorClusterCmd.AddCommand(clusterInstallLogCollectorCmd, clusterLogCollectorReviewCmd, clusterLogCollectorStatusCmd)
}
