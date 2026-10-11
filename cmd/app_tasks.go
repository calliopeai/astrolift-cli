// Package cmd -- `astro app tasks ...`: launch and follow one-shot
// `kind = "task"` workloads (calliopeai/astrolift-cli#151).
//
// `run` calls runTask, which records a TaskRun and dispatches it as a
// Kubernetes Job (astrolift-app#2330); `status` reads astroliftTaskRun(id)
// after the launching process is gone. Both take --wait, which polls the
// run to a terminal status and exits non-zero unless it succeeded, so a CI
// step can gate on a batch workload without hand-written GraphQL.
//
// The run's command is never printed: it is operator input and may carry
// values the caller would not want in a CI log.
package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

// ---- flags -----------------------------------------------------------------

var (
	appTasksWorkload    string
	appTasksEnvironment string
	appTasksRequestID   string
	appTasksWait        bool
	appTasksTimeout     time.Duration
)

// appTasksPollInterval is the cadence --wait re-reads the run. A var so
// tests can shorten it.
var appTasksPollInterval = 5 * time.Second

// terminalTaskRunStatuses mirrors TaskRun.Status's terminal values.
var terminalTaskRunStatuses = map[string]bool{"succeeded": true, "failed": true, "cancelled": true}

// ---- GraphQL operations -----------------------------------------------------

const runTaskMutation = `mutation($input: RunTaskInput!) {
  runTask(input: $input) {
    ok
    errors { code message field }
    data { id }
  }
}`

const taskRunQuery = `query($id: String!) {
  astroliftTaskRun(id: $id) {
    id
    registeredAppSlug
    workloadSlug
    environmentName
    triggerKind
    status
    exitCode
    failureReason
    k8sJobName
    namespace
    createdAt
    startedAt
    endedAt
    durationSeconds
  }
}`

// ---- response shapes (GraphQL camelCase) ------------------------------------

// taskRun is the AstroliftTaskRun projection, minus the command.
type taskRun struct {
	ID              string  `json:"id"`
	App             string  `json:"registeredAppSlug"`
	Workload        string  `json:"workloadSlug"`
	Environment     string  `json:"environmentName"`
	TriggerKind     string  `json:"triggerKind"`
	Status          string  `json:"status"`
	ExitCode        *int    `json:"exitCode"`
	FailureReason   string  `json:"failureReason"`
	JobName         string  `json:"k8sJobName"`
	Namespace       string  `json:"namespace"`
	CreatedAt       string  `json:"createdAt"`
	StartedAt       *string `json:"startedAt"`
	EndedAt         *string `json:"endedAt"`
	DurationSeconds *int    `json:"durationSeconds"`
}

// ---- commands ---------------------------------------------------------------

var appTasksCmd = &cobra.Command{
	Use:   "tasks",
	Short: "Run and follow one-shot task workloads",
	Long: `Launch a manifest-declared task workload (kind = "task") as a Kubernetes
Job and follow it to completion.

  # run a batch job in staging, wait, and fail the CI step if it fails
  astro app tasks run --app reports --workload nightly-export \
      --environment staging --request-id "$GITHUB_RUN_ID" --wait

  # override the declared command (everything after --)
  astro app tasks run --workload db-migrate --wait -- python manage.py migrate

  # check a run later, from another process
  astro app tasks status <run-id> --json`,
}

var appTasksRunCmd = &cobra.Command{
	Use:   "run --workload <slug> [-- command...]",
	Short: "Launch a task run",
	Long: `Calls runTask for one task workload. The app comes from --app or the
astrolift.toml in the current directory. --environment may be omitted when
the app has exactly one environment. Arguments after -- replace the task's
declared command for this run.

--request-id makes retries safe: a second launch with the same id returns
the original run instead of starting another Job. Use a value that is stable
across retries of one pipeline step, such as the CI run id.

With --wait, polls until the run succeeds, fails or is cancelled, and exits
non-zero unless it succeeded or if --timeout passes first.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 && cmd.ArgsLenAtDash() != 0 {
			return fmt.Errorf("put the command override after --, e.g. astro app tasks run --workload %s -- %s", dashIfEmpty(appTasksWorkload), strings.Join(args, " "))
		}
		slug, err := resolveAppSlug(cmd, "")
		if err != nil {
			return err
		}
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAppTasksRun(cmd, cmd.Context(), client, slug, args)
	},
}

var appTasksStatusCmd = &cobra.Command{
	Use:   "status <run-id>",
	Short: "Show a task run's status",
	Long: `Reads one task run by id. Exits non-zero when the run failed or was
cancelled. With --wait, polls until the run is terminal first.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAppTasksStatus(cmd, cmd.Context(), client, args[0])
	},
}

func runAppTasksRun(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug string, command []string) error {
	workload := strings.TrimSpace(appTasksWorkload)
	if workload == "" {
		return fmt.Errorf("--workload is required")
	}
	if appTasksWait && appTasksTimeout <= 0 {
		return fmt.Errorf("--timeout must be positive")
	}
	input := map[string]interface{}{"appSlug": appSlug, "workloadSlug": workload, "command": command}
	if command == nil {
		input["command"] = []string{}
	}
	if v := strings.TrimSpace(appTasksEnvironment); v != "" {
		input["environmentName"] = v
	}
	if v := strings.TrimSpace(appTasksRequestID); v != "" {
		input["requestId"] = v
	}

	var resp struct {
		RunTask struct {
			OK     bool            `json:"ok"`
			Errors []mutationError `json:"errors"`
			Data   *struct {
				ID string `json:"id"`
			} `json:"data"`
		} `json:"runTask"`
	}
	if err := client.GraphQL(ctx, runTaskMutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("running task %s/%s: %w", appSlug, workload, err)
	}
	if !resp.RunTask.OK || resp.RunTask.Data == nil {
		return fmt.Errorf("running task %s/%s: %s", appSlug, workload, firstDeployError(resp.RunTask.Errors))
	}
	if !boolFlag(cmd, "json") {
		fmt.Fprintf(cmd.ErrOrStderr(), "Started task run %s\n", resp.RunTask.Data.ID)
	}
	return showTaskRun(cmd, ctx, client, resp.RunTask.Data.ID)
}

func runAppTasksStatus(cmd *cobra.Command, ctx context.Context, client *api.Client, runID string) error {
	if appTasksWait && appTasksTimeout <= 0 {
		return fmt.Errorf("--timeout must be positive")
	}
	return showTaskRun(cmd, ctx, client, strings.TrimSpace(runID))
}

// showTaskRun reads the run (polling to terminal under --wait), renders it,
// and turns a failed, cancelled or still-running-at-timeout run into an error.
func showTaskRun(cmd *cobra.Command, ctx context.Context, client *api.Client, runID string) error {
	run, err := fetchTaskRun(ctx, client, runID)
	if err != nil {
		return err
	}
	timedOut := false
	if appTasksWait {
		deadline := time.Now().Add(appTasksTimeout)
		for !terminalTaskRunStatuses[run.Status] {
			if !time.Now().Before(deadline) {
				timedOut = true
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(appTasksPollInterval):
			}
			if run, err = fetchTaskRun(ctx, client, runID); err != nil {
				return err
			}
		}
	}

	if boolFlag(cmd, "json") {
		if err := renderJSON(cmd, run); err != nil {
			return err
		}
	} else {
		renderTaskRun(cmd, run)
	}

	switch {
	case timedOut:
		return fmt.Errorf("task run %s still %s after %s", run.ID, run.Status, appTasksTimeout)
	case run.Status == "failed" || run.Status == "cancelled":
		if run.FailureReason != "" {
			return fmt.Errorf("task run %s %s: %s", run.ID, run.Status, run.FailureReason)
		}
		return fmt.Errorf("task run %s %s", run.ID, run.Status)
	}
	return nil
}

func fetchTaskRun(ctx context.Context, client *api.Client, runID string) (*taskRun, error) {
	var resp struct {
		Run *taskRun `json:"astroliftTaskRun"`
	}
	if err := client.GraphQL(ctx, taskRunQuery, map[string]interface{}{"id": runID}, &resp); err != nil {
		return nil, fmt.Errorf("reading task run %s: %w", runID, err)
	}
	if resp.Run == nil {
		return nil, fmt.Errorf("task run %s not found", runID)
	}
	return resp.Run, nil
}

func renderTaskRun(cmd *cobra.Command, run *taskRun) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Run:         %s\n", run.ID)
	fmt.Fprintf(out, "Task:        %s/%s\n", run.App, run.Workload)
	fmt.Fprintf(out, "Environment: %s\n", dashIfEmpty(run.Environment))
	fmt.Fprintf(out, "Status:      %s\n", run.Status)
	if run.ExitCode != nil {
		fmt.Fprintf(out, "Exit code:   %d\n", *run.ExitCode)
	}
	fmt.Fprintf(out, "Started:     %s\n", shortTime(run.StartedAt))
	fmt.Fprintf(out, "Ended:       %s\n", shortTime(run.EndedAt))
	if run.DurationSeconds != nil {
		fmt.Fprintf(out, "Duration:    %ds\n", *run.DurationSeconds)
	}
	if run.JobName != "" {
		fmt.Fprintf(out, "Job:         %s/%s\n", dashIfEmpty(run.Namespace), run.JobName)
	}
}

func init() {
	appTasksRunCmd.Flags().StringVar(&appTasksWorkload, "workload", "", "Task workload slug (required)")
	appTasksRunCmd.Flags().StringVar(&appTasksEnvironment, "environment", "", "Target environment (default: the app's only environment)")
	appTasksRunCmd.Flags().StringVar(&appTasksRequestID, "request-id", "", "Idempotency key; a retry with the same key returns the original run")
	for _, c := range []*cobra.Command{appTasksRunCmd, appTasksStatusCmd} {
		c.Flags().BoolVar(&appTasksWait, "wait", false, "Poll until the run succeeds, fails or is cancelled")
		c.Flags().DurationVar(&appTasksTimeout, "timeout", 30*time.Minute, "Maximum time --wait polls")
	}
	appTasksCmd.AddCommand(appTasksRunCmd, appTasksStatusCmd)
	appCmd.AddCommand(appTasksCmd)
}
