package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/google/uuid"
)

const execEnvironmentsQuery = `query($app: String!) {
  astroliftEnvironments(appSlug: $app) { id name }
}`

const execEnvironmentPodsQuery = `query($app: String!, $environment: String!) {
  astroliftAppPods(appSlug: $app, environmentName: $environment) {
    name workload phase ready containerStatuses { name kind }
  }
}`

const execEnvironmentReviewQuery = `query($app: String!, $workload: String!, $environment: GUID!, $pod: String!, $container: String!) {
  astroliftAppExecTarget(appSlug: $app, workloadSlug: $workload, environmentId: $environment, podName: $pod, container: $container) {
    workloadId workloadVersion appId appVersion environmentId environmentName environmentVersion
    clusterId clusterVersion namespace podName podUid container podBinding resumable
  }
}`

type execEnvironmentTarget struct {
	environmentActionTarget
	PodName    string `json:"podName"`
	PodUID     string `json:"podUid"`
	Container  string `json:"container"`
	PodBinding string `json:"podBinding"`
	Resumable  *bool  `json:"resumable"`
}

func (t execEnvironmentTarget) validate() error {
	if err := t.environmentActionTarget.validate(); err != nil {
		return err
	}
	if t.PodName == "" || t.PodUID == "" || t.Container == "" || t.PodBinding != "PREFLIGHT_ONLY" || t.Resumable == nil || *t.Resumable {
		return fmt.Errorf("server omitted exact pod/container identity or advertised unsupported session authority")
	}
	return nil
}

func reviewExecEnvironment(ctx context.Context, client *api.Client) (*execEnvironmentTarget, error) {
	if publicServerURL(client.BaseURL()) != client.BaseURL() {
		return nil, fmt.Errorf("exec requires a server URL without embedded credentials, query or fragment")
	}
	if _, err := uuid.Parse(execEnvironment); err != nil {
		return nil, fmt.Errorf("--environment requires an immutable environment GUID")
	}
	if execPod != "" && execWorkload != "" && execContainer != "" {
		return requestExecEnvironmentReview(ctx, client, execWorkload, execPod, execContainer, "")
	}
	var environments struct {
		Rows []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"astroliftEnvironments"`
	}
	if err := client.GraphQL(ctx, execEnvironmentsQuery, map[string]interface{}{"app": execApp}, &environments); err != nil {
		return nil, fmt.Errorf("reading selected environment: %w", err)
	}
	name := ""
	for _, row := range environments.Rows {
		if row.ID == execEnvironment {
			if name != "" {
				return nil, fmt.Errorf("environment GUID is ambiguous")
			}
			name = row.Name
		}
	}
	if name == "" {
		return nil, fmt.Errorf("selected environment is unavailable in this app; no primary fallback")
	}
	var pods struct {
		Rows []execPodInfo `json:"astroliftAppPods"`
	}
	if err := client.GraphQL(ctx, execEnvironmentPodsQuery, map[string]interface{}{"app": execApp, "environment": name}, &pods); err != nil {
		return nil, fmt.Errorf("listing selected environment pods: %w", err)
	}
	where := fmt.Sprintf("app %q, environment %q (%s)", execApp, name, execEnvironment)
	var candidates []execPodInfo
	var selected *execPodInfo
	for i := range pods.Rows {
		pod := &pods.Rows[i]
		if execWorkload != "" && pod.Workload != execWorkload {
			continue
		}
		if execPod != "" {
			if pod.Name == execPod {
				selected = pod
				break
			}
			candidates = append(candidates, *pod)
			continue
		}
		if pod.readyAndRunning() {
			selected = pod
			break
		}
		candidates = append(candidates, *pod)
	}
	if selected == nil {
		return nil, fmt.Errorf("%w; no primary fallback", noExecPodError(where, pods.Rows, candidates))
	}
	if selected.Workload == "" {
		return nil, fmt.Errorf("pod %q in %s reports no workload, which a reviewed target needs; no primary fallback", selected.Name, where)
	}
	container := execContainer
	if container == "" {
		container = selected.primaryContainer()
	}
	containerFound := false
	var containers []string
	for _, c := range selected.ContainerStatuses {
		containers = append(containers, c.Name)
		if c.Name == container {
			containerFound = true
		}
	}
	if !containerFound {
		return nil, fmt.Errorf("container %q is not in pod %q (containers: %s); no container fallback",
			container, selected.Name, strings.Join(containers, ", "))
	}
	return requestExecEnvironmentReview(ctx, client, selected.Workload, selected.Name, container, name)
}

func requestExecEnvironmentReview(ctx context.Context, client *api.Client, workload, pod, container, environmentName string) (*execEnvironmentTarget, error) {
	var response struct {
		Target *execEnvironmentTarget `json:"astroliftAppExecTarget"`
	}
	vars := map[string]interface{}{"app": execApp, "workload": workload, "environment": execEnvironment, "pod": pod, "container": container}
	if err := client.GraphQL(ctx, execEnvironmentReviewQuery, vars, &response); err != nil {
		return nil, fmt.Errorf("reviewing selected exec target: %w", err)
	}
	if response.Target == nil {
		return nil, fmt.Errorf("selected exec target is unavailable; no primary fallback")
	}
	if err := response.Target.validate(); err != nil {
		return nil, err
	}
	if response.Target.EnvironmentID != execEnvironment || (environmentName != "" && response.Target.EnvironmentName != environmentName) || response.Target.PodName != pod || response.Target.Container != container {
		return nil, fmt.Errorf("server returned a different exec target")
	}
	return response.Target, nil
}
