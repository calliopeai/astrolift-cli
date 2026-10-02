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
    name workload phase ready containerStatuses { name }
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
			continue
		}
		if pod.Ready && strings.EqualFold(pod.Phase, "Running") {
			selected = pod
			break
		}
	}
	if selected == nil || selected.Workload == "" {
		return nil, fmt.Errorf("no requested pod/workload in the selected environment; no primary fallback")
	}
	container := execContainer
	if container == "" && len(selected.ContainerStatuses) > 0 {
		container = selected.ContainerStatuses[0].Name
	}
	containerFound := false
	for _, c := range selected.ContainerStatuses {
		if c.Name == container {
			containerFound = true
		}
	}
	if !containerFound {
		return nil, fmt.Errorf("selected container is unavailable; no container fallback")
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
