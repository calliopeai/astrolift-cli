package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const environmentWorkloadsQuery = `query EnvironmentActionWorkload($app: String!, $workload: String!) {
  astroliftApp(slug: $app) { id slug }
  astroliftWorkload(appSlug: $app, slug: $workload) { id slug registeredAppSlug }
}`

func environmentSelectorIDs(cmd *cobra.Command, client *api.Client, app, workload string) (string, string, error) {
	organization := client.Org()
	var response struct {
		App *struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"astroliftApp"`
		Workload *struct {
			ID      string `json:"id"`
			Slug    string `json:"slug"`
			AppSlug string `json:"registeredAppSlug"`
		} `json:"astroliftWorkload"`
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	if err := client.GraphQL(ctx, environmentWorkloadsQuery, map[string]interface{}{"app": app, "workload": workload}, &response); err != nil {
		return "", "", fmt.Errorf("finding selected app/workload identities: %w", err)
	}
	if client.Org() != organization {
		return "", "", fmt.Errorf("organization changed during workload review")
	}
	if response.App == nil || response.App.Slug != app {
		return "", "", fmt.Errorf("selected app is unavailable or mismatched")
	}
	if _, err := uuid.Parse(response.App.ID); err != nil {
		return "", "", fmt.Errorf("selected app lacks an immutable GUID")
	}
	if response.Workload == nil {
		return "", "", fmt.Errorf("workload not found in the selected app")
	}
	if _, err := uuid.Parse(response.Workload.ID); err != nil || response.Workload.Slug != workload || response.Workload.AppSlug != app {
		return "", "", fmt.Errorf("server returned an invalid or different app/workload target")
	}
	return response.App.ID, response.Workload.ID, nil
}

const environmentActionReviewQuery = `query($workload: GUID!, $environment: GUID!) {
  astroliftWorkloadActionTarget(workloadId: $workload, environmentId: $environment) {
    workloadId workloadVersion appId appVersion environmentId environmentName environmentVersion
    clusterId clusterVersion namespace
    viewerCan { restart { allowed code reason } scale { allowed code reason } }
  }
}`

const restartReviewedWorkloadMutation = `mutation($input: RestartWorkloadInput!, $version: Int!) {
  restartAstroliftWorkload(input: $input, ifMatchVersion: $version) {
    ok errors { code message currentVersion requestedVersion }
    data { operationId accepted completed workloadVersion desiredReplicas readyReplicas
      target { workloadId appId environmentId clusterId namespace } }
  }
}`

const scaleReviewedWorkloadMutation = `mutation($input: ScaleWorkloadInput!, $version: Int!) {
  scaleAstroliftWorkload(input: $input, ifMatchVersion: $version) {
    ok errors { code message currentVersion requestedVersion }
    data { operationId accepted completed workloadVersion desiredReplicas readyReplicas
      target { workloadId appId environmentId clusterId namespace } }
  }
}`

type environmentActionTarget struct {
	WorkloadID         string `json:"workloadId"`
	WorkloadVersion    *int   `json:"workloadVersion"`
	AppID              string `json:"appId"`
	AppVersion         *int   `json:"appVersion"`
	EnvironmentID      string `json:"environmentId"`
	EnvironmentName    string `json:"environmentName"`
	EnvironmentVersion *int   `json:"environmentVersion"`
	ClusterID          string `json:"clusterId"`
	ClusterVersion     *int   `json:"clusterVersion"`
	Namespace          string `json:"namespace"`
}

func (t environmentActionTarget) validate() error {
	for _, id := range []string{t.WorkloadID, t.AppID, t.EnvironmentID, t.ClusterID} {
		if _, err := uuid.Parse(id); err != nil {
			return fmt.Errorf("review requires immutable GUIDs")
		}
	}
	for _, v := range []*int{t.WorkloadVersion, t.AppVersion, t.EnvironmentVersion, t.ClusterVersion} {
		if v == nil || *v < 0 {
			return fmt.Errorf("review requires nonnegative workload/app/environment/cluster versions")
		}
	}
	if t.Namespace == "" || t.EnvironmentName == "" {
		return fmt.Errorf("review requires environment and namespace")
	}
	return nil
}

type environmentActionReview struct {
	Format       int                     `json:"format"`
	Server       string                  `json:"server"`
	Organization string                  `json:"organizationId"`
	Actor        int                     `json:"actorId"`
	AppSlug      string                  `json:"appSlug"`
	WorkloadSlug string                  `json:"workloadSlug"`
	Target       environmentActionTarget `json:"target"`
	ViewerCan    json.RawMessage         `json:"viewerCan"`
}

func runEnvironmentActionReview(cmd *cobra.Command, client *api.Client, app, workload, environment string) error {
	if _, err := uuid.Parse(environment); err != nil {
		return fmt.Errorf("--environment requires an immutable environment GUID")
	}
	if client.Org() == "" {
		return fmt.Errorf("select an organization before reviewing an environment")
	}
	if publicServerURL(client.BaseURL()) != client.BaseURL() {
		return fmt.Errorf("review requires a server URL without embedded credentials, query or fragment")
	}
	actor, err := fetchPermissionIdentity(cmd, client)
	if err != nil {
		return err
	}
	appID, id, err := environmentSelectorIDs(cmd, client, app, workload)
	if err != nil {
		return err
	}
	var response struct {
		Target *struct {
			environmentActionTarget
			ViewerCan json.RawMessage `json:"viewerCan"`
		} `json:"astroliftWorkloadActionTarget"`
	}
	if err := client.GraphQL(cmd.Context(), environmentActionReviewQuery, map[string]interface{}{"workload": id, "environment": environment}, &response); err != nil {
		return fmt.Errorf("reviewing exact target: %w", err)
	}
	if response.Target == nil {
		return fmt.Errorf("selected environment target is unavailable; no primary fallback")
	}
	if err := response.Target.validate(); err != nil {
		return err
	}
	if response.Target.AppID != appID || response.Target.WorkloadID != id || response.Target.EnvironmentID != environment {
		return fmt.Errorf("server returned a different target")
	}
	return renderJSON(cmd, environmentActionReview{Format: 1, Server: client.BaseURL(), Organization: client.Org(), Actor: actor.UserID, AppSlug: app, WorkloadSlug: workload, Target: response.Target.environmentActionTarget, ViewerCan: response.Target.ViewerCan})
}

func loadEnvironmentActionReview(path string) (environmentActionReview, error) {
	var review environmentActionReview
	if path == "" {
		return review, fmt.Errorf("--review is required; run app workload review first")
	}
	file, err := os.Open(path)
	if err != nil {
		return review, fmt.Errorf("reading review: %w", err)
	}
	defer func() { _ = file.Close() }()
	if info, err := file.Stat(); err != nil {
		return review, err
	} else if info.Size() > 65536 {
		return review, fmt.Errorf("review exceeds 64 KiB")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 65537))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&review); err != nil {
		return review, fmt.Errorf("invalid review: %w", err)
	}
	if err := decoder.Decode(new(interface{})); err != io.EOF {
		return review, fmt.Errorf("review must contain exactly one JSON object")
	}
	if review.Format != 1 || review.Server == "" || review.Organization == "" || review.Actor <= 0 || review.AppSlug == "" || review.WorkloadSlug == "" {
		return review, fmt.Errorf("review lacks its server, organization, actor or selectors")
	}
	if publicServerURL(review.Server) != review.Server {
		return review, fmt.Errorf("review server URL must not contain credentials, query or fragment")
	}
	return review, review.Target.validate()
}

func runReviewedEnvironmentAction(cmd *cobra.Command, client *api.Client, app, workload, environment, path, action string, replicas int, yes bool) error {
	review, err := loadEnvironmentActionReview(path)
	if err != nil {
		return err
	}
	if review.Server != client.BaseURL() || review.Organization != client.Org() || review.AppSlug != app || review.WorkloadSlug != workload || review.Target.EnvironmentID != environment {
		return fmt.Errorf("review does not match the selected server, organization, app, workload and environment")
	}
	actor, err := fetchPermissionIdentity(cmd, client)
	if err != nil {
		return err
	}
	if actor.UserID != review.Actor {
		return fmt.Errorf("review belongs to another actor; review again")
	}
	appID, workloadID, err := environmentSelectorIDs(cmd, client, app, workload)
	if err != nil {
		return err
	}
	if appID != review.Target.AppID || workloadID != review.Target.WorkloadID {
		return fmt.Errorf("reviewed app/workload was replaced or does not match the selected selectors")
	}
	if !yes {
		if boolFlag(cmd, "no-prompt") {
			return fmt.Errorf("confirmation required: pass --yes after inspecting the review")
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "%s %s/%s in %s (%s), cluster %s, namespace %s? [y/N] ", action, app, workload, review.Target.EnvironmentName, environment, review.Target.ClusterID, review.Target.Namespace)
		var answer string
		if _, err := fmt.Fscan(cmd.InOrStdin(), &answer); err != nil {
			return fmt.Errorf("reading confirmation: %w", err)
		}
		if strings.ToLower(answer) != "y" {
			return fmt.Errorf("action cancelled")
		}
	}
	t := review.Target
	input := map[string]interface{}{"workloadId": t.WorkloadID, "environmentId": t.EnvironmentID,
		"ifMatchAppVersion": *t.AppVersion, "ifMatchEnvironmentVersion": *t.EnvironmentVersion,
		"expectedClusterId": t.ClusterID, "ifMatchClusterVersion": *t.ClusterVersion, "expectedNamespace": t.Namespace}
	query := restartReviewedWorkloadMutation
	if action == "scale" {
		query = scaleReviewedWorkloadMutation
		input["replicas"] = replicas
	}
	var response map[string]struct {
		Ok     bool `json:"ok"`
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Data json.RawMessage `json:"data"`
	}
	if err := client.GraphQL(cmd.Context(), query, map[string]interface{}{"input": input, "version": *t.WorkloadVersion}, &response); err != nil {
		return fmt.Errorf("%s request failed; inspect live state before repeating: %w", action, err)
	}
	field := "restartAstroliftWorkload"
	if action == "scale" {
		field = "scaleAstroliftWorkload"
	}
	result, found := response[field]
	if !found || !result.Ok {
		if len(result.Errors) > 0 {
			return fmt.Errorf("%s refused (%s): %s", action, result.Errors[0].Code, result.Errors[0].Message)
		}
		return fmt.Errorf("%s returned no successful result", action)
	}
	if len(result.Data) == 0 || string(result.Data) == "null" {
		return fmt.Errorf("server accepted action but omitted its receipt; inspect live state")
	}
	var receipt struct {
		OperationID     string `json:"operationId"`
		Accepted        bool   `json:"accepted"`
		Completed       *bool  `json:"completed"`
		WorkloadVersion *int   `json:"workloadVersion"`
		Target          *struct {
			WorkloadID    string `json:"workloadId"`
			AppID         string `json:"appId"`
			EnvironmentID string `json:"environmentId"`
			ClusterID     string `json:"clusterId"`
			Namespace     string `json:"namespace"`
		} `json:"target"`
	}
	if err := json.Unmarshal(result.Data, &receipt); err != nil {
		return fmt.Errorf("invalid action receipt; inspect live state: %w", err)
	}
	if _, err := uuid.Parse(receipt.OperationID); err != nil || !receipt.Accepted || receipt.Completed != nil || receipt.WorkloadVersion == nil || receipt.Target == nil || receipt.Target.WorkloadID != t.WorkloadID || receipt.Target.AppID != t.AppID || receipt.Target.EnvironmentID != t.EnvironmentID || receipt.Target.ClusterID != t.ClusterID || receipt.Target.Namespace != t.Namespace {
		return fmt.Errorf("server returned an incomplete or mismatched action receipt; inspect live state")
	}
	return renderJSON(cmd, result.Data)
}

func init() {
	group := &cobra.Command{Use: "workload", Short: "Review an exact environment before restart or scale"}
	for _, action := range []string{"review", "restart", "scale"} {
		action := action
		var environment, path string
		var yes bool
		use := action + " <workload-slug>"
		argsCount := 1
		if action == "scale" {
			use += " <replicas>"
			argsCount = 2
		}
		command := &cobra.Command{Use: use, Short: action + " an explicitly selected workload environment", Args: cobra.ExactArgs(argsCount), RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := uuid.Parse(environment); err != nil {
				return fmt.Errorf("--environment requires an immutable environment GUID")
			}
			replicas := 0
			if action == "scale" {
				var err error
				replicas, err = strconv.Atoi(args[1])
				if err != nil || replicas < 0 || replicas > 20 {
					return fmt.Errorf("replicas must be an integer from 0 through 20; environment limits may be lower")
				}
			}
			client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
			if err != nil {
				return err
			}
			if _, err := resolveOrg(cmd, cmd.Context(), client, cfg); err != nil {
				return err
			}
			app, err := resolveAppSlug(cmd, "")
			if err != nil {
				return err
			}
			if action == "review" {
				return runEnvironmentActionReview(cmd, client, app, args[0], environment)
			}
			return runReviewedEnvironmentAction(cmd, client, app, args[0], environment, path, action, replicas, yes)
		}}
		command.Flags().StringVar(&environment, "environment", "", "Exact environment GUID (required; no primary fallback)")
		if action != "review" {
			command.Flags().StringVar(&path, "review", "", "Saved JSON from app workload review (required)")
			command.Flags().BoolVar(&yes, "yes", false, "Confirm the reviewed action")
		}
		group.AddCommand(command)
	}
	appCmd.AddCommand(group)
}
