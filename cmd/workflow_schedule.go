package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const workflowScheduleFields = `workflowId scheduleId configurationVersion desiredRevision desiredActive
 observedState confirmed observedAt errorCode message engineCreatedAt engineUpdatedAt actionCount`

const workflowScheduleQuery = `query($workflowId: GUID!) {
 workflowSchedule(workflowId: $workflowId) { ` + workflowScheduleFields + ` }
}`
const reconcileWorkflowScheduleMutation = `mutation($workflowId: GUID!, $expectedVersion: Int!, $expectedActive: Boolean!) {
 reconcileWorkflowSchedule(workflowId: $workflowId, expectedVersion: $expectedVersion, expectedActive: $expectedActive) {
  ok errors { field messages } configurationSaved schedule { ` + workflowScheduleFields + ` }
 }
}`
const updateConfiguredWorkflowMutation = `mutation($workflowId: GUID!, $isEnabled: Boolean, $scheduleCron: String, $triggerKind: String) {
 updateWorkflow(workflowId: $workflowId, isEnabled: $isEnabled, scheduleCron: $scheduleCron, triggerKind: $triggerKind) {
  ok errors { field messages } configurationSaved
  workflow { guid name slug definitionGuid definitionSlug triggerKind isEnabled runCount }
  schedule { ` + workflowScheduleFields + ` }
 }
}`

type workflowScheduleState struct {
	WorkflowID           string  `json:"workflowId"`
	ScheduleID           string  `json:"scheduleId"`
	ConfigurationVersion int     `json:"configurationVersion"`
	DesiredRevision      string  `json:"desiredRevision"`
	DesiredActive        bool    `json:"desiredActive"`
	ObservedState        string  `json:"observedState"`
	Confirmed            bool    `json:"confirmed"`
	ObservedAt           string  `json:"observedAt"`
	ErrorCode            string  `json:"errorCode"`
	Message              string  `json:"message"`
	EngineCreatedAt      *string `json:"engineCreatedAt"`
	EngineUpdatedAt      *string `json:"engineUpdatedAt"`
	ActionCount          *int    `json:"actionCount"`
}

type workflowConfigurationResult struct {
	Ok                 bool                   `json:"ok"`
	Errors             validationErrors       `json:"errors"`
	ConfigurationSaved *bool                  `json:"configurationSaved"`
	Workflow           *configuredWorkflow    `json:"workflow,omitempty"`
	Schedule           *workflowScheduleState `json:"schedule"`
}

func sameWorkflowGUID(actual, expected string) bool {
	a, err := uuid.Parse(actual)
	if err != nil {
		return false
	}
	b, err := uuid.Parse(expected)
	return err == nil && a == b
}

func workflowGUIDArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.ExactArgs(1)(cmd, args); err != nil {
		return err
	}
	if _, err := uuid.Parse(args[0]); err != nil {
		return fmt.Errorf("an exact configured workflow GUID is required: %w", err)
	}
	return nil
}

func newWorkflowScheduleCmd() *cobra.Command {
	group := &cobra.Command{Use: "schedule", Short: "Inspect and reconcile native workflow schedules"}
	inspect := &cobra.Command{
		Use: "inspect <workflow-guid>", Short: "Observe the exact schedule and reviewed configuration version", Args: workflowGUIDArgs,
		RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
			return runWorkflowScheduleInspect(cmd, ctx, client, args[0])
		}),
	}
	reconcile := &cobra.Command{
		Use: "reconcile <workflow-guid>", Short: "Apply an explicitly reviewed schedule configuration",
		Long: `Reconcile the exact workflow GUID with Temporal using the version and desiredActive
value you reviewed through schedule inspect. A stale review is refused by the server.
Active recovery requires workflow trigger authority. Deleted-row cleanup additionally
requires delete authority. Engine failures retain the exact recovery identity; inspect
again before retrying. This command does not start an immediate run.`,
		Args: workflowGUIDArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			vars, err := workflowScheduleReview(cmd, args[0])
			if err != nil {
				return err
			}
			return workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ []string) error {
				return runWorkflowScheduleReconcile(cmd, ctx, client, vars)
			})(cmd, args)
		},
	}
	reconcile.Flags().Int("expected-version", 0, "Reviewed configurationVersion from schedule inspect (required)")
	reconcile.Flags().Bool("expected-active", false, "Reviewed desiredActive value; pass =true or =false (required)")
	group.AddCommand(inspect, reconcile)
	return group
}

func workflowScheduleReview(cmd *cobra.Command, id string) (map[string]interface{}, error) {
	if err := workflowGUIDArgs(cmd, []string{id}); err != nil {
		return nil, err
	}
	version, err := cmd.Flags().GetInt("expected-version")
	if err != nil {
		return nil, err
	}
	if !cmd.Flags().Changed("expected-version") || version < 1 || int64(version) > 2147483647 {
		return nil, fmt.Errorf("--expected-version must be the positive GraphQL Int configurationVersion you reviewed")
	}
	if !cmd.Flags().Changed("expected-active") {
		return nil, fmt.Errorf("--expected-active=true or --expected-active=false is required")
	}
	active, err := cmd.Flags().GetBool("expected-active")
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"workflowId": id, "expectedVersion": version, "expectedActive": active}, nil
}

func runWorkflowScheduleInspect(cmd *cobra.Command, ctx context.Context, client *api.Client, id string) error {
	if err := workflowGUIDArgs(cmd, []string{id}); err != nil {
		return err
	}
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var resp struct {
		Schedule *workflowScheduleState `json:"workflowSchedule"`
	}
	if err := client.GraphQL(readCtx, workflowScheduleQuery, map[string]interface{}{"workflowId": id}, &resp); err != nil {
		return fmt.Errorf("inspecting workflow schedule: %w", err)
	}
	if boolFlag(cmd, "json") {
		if err := renderJSON(cmd, resp); err != nil {
			return err
		}
	} else if resp.Schedule != nil {
		printWorkflowSchedule(cmd, resp.Schedule)
	}
	if resp.Schedule == nil {
		return fmt.Errorf("no visible workflow schedule configuration for %s", id)
	}
	if !sameWorkflowGUID(resp.Schedule.WorkflowID, id) {
		return fmt.Errorf("server returned a different workflow identity")
	}
	if resp.Schedule.ErrorCode != "" {
		return fmt.Errorf("schedule observation is unconfirmed (%s)", resp.Schedule.ErrorCode)
	}
	return nil
}

func runWorkflowScheduleReconcile(cmd *cobra.Command, ctx context.Context, client *api.Client, vars map[string]interface{}) error {
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var resp struct {
		Result workflowConfigurationResult `json:"reconcileWorkflowSchedule"`
	}
	if err := client.GraphQL(callCtx, reconcileWorkflowScheduleMutation, vars, &resp); err != nil {
		return fmt.Errorf("reconciling workflow schedule for %s: %w; inspect this ID before retrying", vars["workflowId"], err)
	}
	return reportWorkflowConfiguration(cmd, resp.Result, "reconcile", fmt.Sprint(vars["workflowId"]))
}

func printWorkflowSchedule(cmd *cobra.Command, s *workflowScheduleState) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Workflow ID: %s\nSchedule ID: %s\nConfiguration version: %d\nDesired active: %t\nObserved state: %s\nConfirmed: %t\nObserved at: %s\n", s.WorkflowID, s.ScheduleID, s.ConfigurationVersion, s.DesiredActive, s.ObservedState, s.Confirmed, s.ObservedAt)
	if s.ErrorCode != "" {
		fmt.Fprintf(out, "Error: %s\n", s.ErrorCode)
	}
	if s.Message != "" {
		fmt.Fprintln(out, s.Message)
	}
	if !s.Confirmed && s.ObservedState != "not_requested" {
		fmt.Fprintf(out, "Inspect for recovery: astro workflow schedule inspect %s\n", s.WorkflowID)
	}
}

func reportWorkflowConfiguration(cmd *cobra.Command, result workflowConfigurationResult, operation, target string) error {
	if boolFlag(cmd, "json") {
		if err := renderJSON(cmd, result); err != nil {
			return err
		}
	} else {
		if result.ConfigurationSaved != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Configuration saved: %t\n", *result.ConfigurationSaved)
		}
		if result.Workflow != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Workflow: %s (%s)\nID: %s\n", result.Workflow.Name, result.Workflow.Slug, result.Workflow.GUID)
		}
		if result.Schedule != nil {
			printWorkflowSchedule(cmd, result.Schedule)
		}
	}
	return validateWorkflowConfiguration(result, operation, target)
}

func validateWorkflowConfiguration(result workflowConfigurationResult, operation, target string) error {
	if !result.Ok {
		identity := ""
		if result.Schedule != nil {
			identity = " (workflow " + result.Schedule.WorkflowID + ")"
		}
		return fmt.Errorf("%s failed%s: %s", operation, identity, firstValidationError(result.Errors))
	}
	if result.Schedule == nil || result.ConfigurationSaved == nil {
		return fmt.Errorf("%s returned no schedule receipt; use a server supporting native schedule lifecycle and inspect the saved workflow before retrying", operation)
	}
	if result.Schedule.WorkflowID == "" || result.Schedule.ScheduleID == "" || result.Schedule.ConfigurationVersion < 1 {
		return fmt.Errorf("%s returned an incomplete schedule identity; inspect the saved workflow before retrying", operation)
	}

	if target != "" && !sameWorkflowGUID(result.Schedule.WorkflowID, target) {
		return fmt.Errorf("%s returned a different workflow identity", operation)
	}
	if result.Schedule.ErrorCode != "" {
		return fmt.Errorf("%s is unconfirmed (%s)", operation, result.Schedule.ErrorCode)
	}
	if operation != "reconcile" && !*result.ConfigurationSaved {
		return fmt.Errorf("%s did not confirm configuration persistence", operation)
	}
	observed := result.Schedule
	confirmed := observed.Confirmed && ((observed.DesiredActive && observed.ObservedState == "active") || (!observed.DesiredActive && observed.ObservedState == "missing"))
	draft := operation != "reconcile" && !observed.DesiredActive && observed.ObservedState == "not_requested"
	if !confirmed && !draft {
		return fmt.Errorf("%s schedule is unconfirmed; inspect workflow %s before retrying", operation, observed.WorkflowID)
	}
	return nil
}

func newWorkflowUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "update <workflow-guid>", Short: "Update native schedule configuration by exact workflow ID", Args: workflowGUIDArgs}
	cmd.Flags().Bool("enabled", false, "Set configured workflow enablement explicitly")
	cmd.Flags().String("cron", "", "Set the schedule cron expression; an empty value clears it")
	cmd.Flags().String("trigger", "", "Set trigger kind: manual or schedule")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		vars, err := workflowUpdateVariables(cmd, args[0])
		if err != nil {
			return err
		}
		return workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ []string) error {
			return runWorkflowUpdate(cmd, ctx, client, vars)
		})(cmd, args)
	}
	return cmd
}

func workflowUpdateVariables(cmd *cobra.Command, id string) (map[string]interface{}, error) {
	if err := workflowGUIDArgs(cmd, []string{id}); err != nil {
		return nil, err
	}
	vars := map[string]interface{}{"workflowId": id}
	if cmd.Flags().Changed("enabled") {
		vars["isEnabled"] = boolFlag(cmd, "enabled")
	}
	if cmd.Flags().Changed("cron") {
		value, err := cmd.Flags().GetString("cron")
		if err != nil {
			return nil, err
		}
		vars["scheduleCron"] = value
	}
	if cmd.Flags().Changed("trigger") {
		value, err := cmd.Flags().GetString("trigger")
		if err != nil {
			return nil, err
		}
		if value != "manual" && value != "schedule" {
			return nil, fmt.Errorf("--trigger must be manual or schedule")
		}
		vars["triggerKind"] = value
	}
	if len(vars) == 1 {
		return nil, fmt.Errorf("specify at least one of --enabled, --cron or --trigger")
	}
	return vars, nil
}

func runWorkflowUpdate(cmd *cobra.Command, ctx context.Context, client *api.Client, vars map[string]interface{}) error {
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var resp struct {
		Result workflowConfigurationResult `json:"updateWorkflow"`
	}
	if err := client.GraphQL(callCtx, updateConfiguredWorkflowMutation, vars, &resp); err != nil {
		return fmt.Errorf("updating workflow %s: %w; inspect this ID before retrying", vars["workflowId"], err)
	}
	return reportWorkflowConfiguration(cmd, resp.Result, "update", fmt.Sprint(vars["workflowId"]))
}

func workflowDeleteTarget(cmd *cobra.Command, args []string) (map[string]interface{}, error) {
	id, _ := cmd.Flags().GetString("workflow-id")
	if id != "" {
		if err := workflowGUIDArgs(cmd, []string{id}); err != nil {
			return nil, err
		}
		if len(args) > 0 {
			return nil, fmt.Errorf("use either --workflow-id or a legacy slug")
		}
		return map[string]interface{}{"workflowId": id}, nil
	}
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return nil, fmt.Errorf("a workflow slug or --workflow-id is required")
	}
	return map[string]interface{}{"slug": args[0]}, nil
}

func init() { workflowCmd.AddCommand(newWorkflowScheduleCmd(), newWorkflowUpdateCmd()) }
