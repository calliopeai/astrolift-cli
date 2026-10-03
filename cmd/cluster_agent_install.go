package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const clusterAgentInstallKind = "cluster-agent-install"
const clusterAgentInstallFields = `id clusterId requestId status secretConfirmed deploymentConfirmed heartbeatConfirmed retryable workflowId errorCode errorMessage createdAt updatedAt`

type clusterAgentInstall struct {
	ID                  string `json:"id"`
	ClusterID           string `json:"clusterId"`
	RequestID           string `json:"requestId"`
	Status              string `json:"status"`
	SecretConfirmed     bool   `json:"secretConfirmed"`
	DeploymentConfirmed bool   `json:"deploymentConfirmed"`
	HeartbeatConfirmed  bool   `json:"heartbeatConfirmed"`
	Retryable           bool   `json:"retryable"`
	WorkflowID          string `json:"workflowId"`
	ErrorCode           string `json:"errorCode"`
	ErrorMessage        string `json:"errorMessage"`
	CreatedAt           string `json:"createdAt"`
	UpdatedAt           string `json:"updatedAt"`
}

var clusterInstallAgentCmd = &cobra.Command{
	Use:   "install-agent",
	Short: "Install the cluster agent through the control plane's private network",
	Long: `Installs the keep-alive agent's credential and Deployment using a server-side
workflow. No local kubeconfig or raw agent key is required. The control plane keeps
the current key working until an authenticated heartbeat confirms the replacement.

--request-file stores only the reviewed cluster identity, version, source, request UUID,
server, organization and caller identity, before dispatch. Keep this private file;
retry with the same file after a lost reply to recover the original operation.
Never replace it while the outcome is uncertain. Acceptance of the request does
not mean the agent is healthy; agent-install-status reports heartbeat confirmation.

Requires cluster.manage (enforced server-side). Example:
  astro operator cluster install-agent --slug production --request-file agent-install.json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runClusterInstallAgent(cmd, cmd.Context(), client, installAgentClusterSlug, installAgentKubeconfig, installAgentInterval)
	},
}

func runClusterInstallAgent(cmd *cobra.Command, ctx context.Context, client *api.Client, slug, kubeconfigOverride string, interval int) error {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return errors.New("--slug is required")
	}
	if kubeconfigOverride != "" {
		return errors.New("install-agent now runs server-side; remove --kubeconfig")
	}
	filename, _ := cmd.Flags().GetString("request-file")
	if strings.TrimSpace(filename) == "" {
		return errors.New("--request-file is required to retain the installation's recovery identity")
	}
	if interval < 0 {
		return errors.New("--interval-seconds cannot be negative")
	}
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	server, actor, err := reviewedRequestScope(cmd, client)
	if err != nil {
		return err
	}
	r, err := readReviewedRequest(filename)
	if err == nil {
		if err := r.checkScope(clusterAgentInstallKind, server, client.Org(), actor); err != nil {
			return err
		}
		if r.TargetName != slug || r.Version < 1 || r.IntervalSeconds < 0 {
			return errors.New("cluster selector or reviewed version differs from the saved installation request")
		}
		if strings.TrimSpace(r.ExpectedSource) == "" {
			return errors.New("saved installation request has no original reviewed source; preserve it while the outcome is uncertain")
		}
		if (interval != 0 || cmd.Flags().Changed("interval-seconds")) && interval != r.IntervalSeconds {
			return errors.New("heartbeat interval differs from the saved installation request")
		}
		requestID, err := uuid.Parse(r.RequestID)
		if err != nil || requestID == uuid.Nil || requestID.String() != r.RequestID {
			return errors.New("saved installation request must have its original canonical nonzero UUID")
		}
		targetID, err := uuid.Parse(r.TargetID)
		if err != nil || targetID == uuid.Nil {
			return errors.New("saved installation request must identify an exact nonzero cluster UUID")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else {
		cluster, err := fetchClusterBySlug(requestCtx, client, slug)
		if err != nil {
			return err
		}
		if cluster.ID == uuid.Nil.String() {
			return errors.New("cluster returned an invalid GUID")
		}
		if _, err := uuid.Parse(cluster.ID); err != nil {
			return errors.New("cluster returned an invalid GUID")
		}
		var review struct {
			Data *struct {
				ClusterID string `json:"clusterId"`
				Version   int    `json:"version"`
				Source    string `json:"source"`
			} `json:"astroliftClusterAgentInstallReview"`
		}
		query := `query ClusterAgentInstallReview($clusterId: GUID!) { astroliftClusterAgentInstallReview(clusterId: $clusterId) { clusterId version source } }`
		if err := client.GraphQL(requestCtx, query, map[string]interface{}{"clusterId": cluster.ID}, &review); err != nil {
			return fmt.Errorf("reviewing cluster agent installation: %w", err)
		}
		if review.Data == nil || review.Data.ClusterID != cluster.ID || review.Data.Version < 1 || strings.TrimSpace(review.Data.Source) == "" {
			return errors.New("the exact cluster installation review is unavailable")
		}
		r = &reviewedStartRequest{
			Format: 1, Kind: clusterAgentInstallKind, Server: server,
			OrganizationID: client.Org(), ActorUserID: actor,
			TargetID: cluster.ID, TargetName: slug,
			RequestID: uuid.NewString(), Version: review.Data.Version, IntervalSeconds: interval,
			ExpectedSource: review.Data.Source,
		}
		if err := createReviewedRequest(filename, *r); err != nil {
			return err
		}
	}
	input := map[string]interface{}{"clusterId": r.TargetID, "expectedVersion": r.Version, "expectedSource": r.ExpectedSource, "requestId": r.RequestID}
	if r.IntervalSeconds > 0 {
		input["intervalSeconds"] = r.IntervalSeconds
	}
	var response struct {
		Result struct {
			noneMutationResult
			Data *clusterAgentInstall `json:"data"`
		} `json:"installClusterAgent"`
	}
	mutation := `mutation InstallClusterAgent($input: InstallClusterAgentInput!) { installClusterAgent(input: $input) { ok errors { code message field } data { ` + clusterAgentInstallFields + ` } } }`
	if err := client.GraphQL(requestCtx, mutation, map[string]interface{}{"input": input}, &response); err != nil {
		return fmt.Errorf("installation outcome is unknown; keep %s and retry install-agent with the same request file: %w", filename, err)
	}
	if !response.Result.Ok {
		return fmt.Errorf("installation request refused: %s; original request retained at %s", firstMutationError(response.Result.Errors), filename)
	}
	if err := checkClusterAgentInstall(response.Result.Data, r.TargetID, r.RequestID); err != nil {
		return err
	}
	return printClusterAgentInstall(cmd, response.Result.Data)
}

func checkClusterAgentInstall(install *clusterAgentInstall, clusterID, requestID string) error {
	if install == nil || install.ClusterID != clusterID || install.RequestID != requestID {
		return errors.New("server returned an unavailable or mismatched agent installation")
	}
	for _, id := range []string{install.ID, install.ClusterID, install.RequestID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil {
			return errors.New("server returned an invalid installation identity")
		}
	}
	switch install.Status {
	case "QUEUED", "INSTALLING", "AWAITING_HEARTBEAT", "SUCCEEDED", "REFUSED", "UNCERTAIN":
	default:
		return errors.New("server returned an unknown installation status")
	}
	if install.HeartbeatConfirmed != (install.Status == "SUCCEEDED") ||
		(install.HeartbeatConfirmed && (!install.SecretConfirmed || !install.DeploymentConfirmed)) ||
		(install.Status == "AWAITING_HEARTBEAT" && (!install.SecretConfirmed || !install.DeploymentConfirmed)) ||
		(install.DeploymentConfirmed && !install.SecretConfirmed) {
		return errors.New("server returned inconsistent agent installation confirmation")
	}
	return nil
}

func printClusterAgentInstall(cmd *cobra.Command, install *clusterAgentInstall) error {
	if boolFlag(cmd, "json") {
		if err := renderJSON(cmd, install); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Agent installation %s: %s (heartbeat confirmed: %t).\n", install.ID, install.Status, install.HeartbeatConfirmed)
		fmt.Fprintf(cmd.OutOrStdout(), "Read status: astro operator cluster agent-install-status --install-id %s\n", install.ID)
		if install.ErrorCode != "" || install.ErrorMessage != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Installation note (%s): %s\n", install.ErrorCode, install.ErrorMessage)
		}
	}
	if install.Status == "REFUSED" || install.Status == "UNCERTAIN" {
		return fmt.Errorf("agent installation is %s (%s); preserve the original request file", install.Status, install.ErrorCode)
	}
	return nil
}

var clusterAgentInstallStatusCmd = &cobra.Command{
	Use: "agent-install-status", Short: "Read an exact server-side agent installation and heartbeat confirmation",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		id, _ := cmd.Flags().GetString("install-id")
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runClusterAgentInstallStatus(cmd, cmd.Context(), client, id)
	},
}

func runClusterAgentInstallStatus(cmd *cobra.Command, ctx context.Context, client *api.Client, id string) error {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil {
		return errors.New("--install-id must be a nonzero UUID")
	}
	id = parsed.String()
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var response struct {
		Data *clusterAgentInstall `json:"astroliftClusterAgentInstall"`
	}
	query := `query ClusterAgentInstallStatus($installId: GUID!) { astroliftClusterAgentInstall(installId: $installId) { ` + clusterAgentInstallFields + ` } }`
	if err := client.GraphQL(readCtx, query, map[string]interface{}{"installId": id}, &response); err != nil {
		return fmt.Errorf("reading agent installation: %w", err)
	}
	if response.Data == nil || response.Data.ID != id {
		return errors.New("the exact agent installation is unavailable")
	}
	if err := checkClusterAgentInstall(response.Data, response.Data.ClusterID, response.Data.RequestID); err != nil {
		return err
	}
	return printClusterAgentInstall(cmd, response.Data)
}

func init() {
	clusterInstallAgentCmd.Flags().String("request-file", "", "Private metadata file retaining the original installation request (required)")
	_ = clusterInstallAgentCmd.MarkFlagRequired("request-file")
	clusterAgentInstallStatusCmd.Flags().String("install-id", "", "Exact installation UUID (required)")
	_ = clusterAgentInstallStatusCmd.MarkFlagRequired("install-id")
	operatorClusterCmd.AddCommand(clusterAgentInstallStatusCmd)
}
