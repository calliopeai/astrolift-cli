package api

import "context"

type NVKEInstallPlanPullRuntimeReview struct {
	NVKEInstallPlanPullSecretAnalysisSource
	AnalysisID                  string `json:"analysisId"`
	ExpectedAnalysisVersion     int    `json:"expectedAnalysisVersion"`
	AnalysisDigest              string `json:"analysisDigest"`
	NodePlatformID              string `json:"nodePlatformId"`
	ExpectedNodePlatformVersion int    `json:"expectedNodePlatformVersion"`
	NodePlatformDigest          string `json:"nodePlatformDigest"`
}

type NVKEInstallPlanPullRuntime struct {
	Analysis               *NVKEInstallPlanPullSecretAnalysis `json:"analysis"`
	NodePlatforms          *NVKEInstallPlanNodePlatforms      `json:"nodePlatforms"`
	CorrespondenceDigest   string                             `json:"correspondenceDigest"`
	ImageUseCount          *int                               `json:"imageUseCount"`
	NodeCount              *int                               `json:"nodeCount"`
	SourceObservedAt       string                             `json:"sourceObservedAt"`
	NodeObservedAt         string                             `json:"nodeObservedAt"`
	ExecutionState         string                             `json:"executionState"`
	ContinuationAuthorized *bool                              `json:"continuationAuthorized"`
}

func (c *Client) ReviewNVKEInstallPlanPullRuntime(ctx context.Context, input NVKEInstallPlanPullRuntimeReview) (*NVKEInstallPlanPullRuntime, error) {
	fields := `correspondenceDigest imageUseCount nodeCount sourceObservedAt nodeObservedAt executionState continuationAuthorized analysis { ` + nvkePullSecretAnalysisFields + ` } nodePlatforms { ` + nvkeNodePlatformsFields + ` }`
	return nvkeMutation[NVKEInstallPlanPullRuntime](ctx, c.nvkeInstallPlanClient(), "reviewNvkeOperatorInstallPlanPullRuntime", "NvkeOperatorInstallPlanPullRuntimeReviewInput", fields, input)
}
