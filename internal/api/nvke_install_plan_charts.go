package api

import "context"

type NVKEInstallPlanChartsSource struct {
	NVKEInstallPlanReview
	ExpectedPlanVersion int    `json:"expectedPlanVersion"`
	PlanDigest          string `json:"planDigest"`
}

type NVKEInstallPlanChartsPrepare struct {
	NVKEInstallPlanChartsSource
	RequestText string `json:"requestText"`
}

type NVKEInstallPlanChartsReview struct {
	NVKEInstallPlanChartsSource
	ArtifactID string `json:"artifactId"`
}

type NVKEInstallPlanCharts struct {
	ID                     string `json:"id"`
	Version                int    `json:"version"`
	Status                 string `json:"status"`
	PlanID                 string `json:"planId"`
	PlanVersion            int    `json:"planVersion"`
	OperationID            string `json:"operationId"`
	OperationVersion       int    `json:"operationVersion"`
	SiteID                 string `json:"siteId"`
	SiteVersion            int    `json:"siteVersion"`
	AuthorityID            string `json:"authorityId"`
	AuthorityVersion       int    `json:"authorityVersion"`
	PlanDigest             string `json:"planDigest"`
	ArtifactDigest         string `json:"artifactDigest"`
	ChartCount             *int   `json:"chartCount"`
	Bytes                  string `json:"bytes"`
	ExpandedBytes          string `json:"expandedBytes"`
	ExecutionState         string `json:"executionState"`
	ContinuationAuthorized *bool  `json:"continuationAuthorized"`
}

const nvkeInstallPlanChartsFields = `id version status planId planVersion operationId operationVersion siteId siteVersion authorityId authorityVersion planDigest artifactDigest chartCount bytes expandedBytes executionState continuationAuthorized`

func (c *Client) PrepareNVKEInstallPlanCharts(ctx context.Context, input NVKEInstallPlanChartsPrepare) (*NVKEInstallPlanCharts, error) {
	return nvkeMutation[NVKEInstallPlanCharts](ctx, c.nvkeInstallPlanClient(), "prepareNvkeOperatorInstallPlanCharts", "PrepareNvkeOperatorInstallPlanChartsInput", nvkeInstallPlanChartsFields, input)
}

func (c *Client) ReviewNVKEInstallPlanCharts(ctx context.Context, input NVKEInstallPlanChartsReview, retry bool) (*NVKEInstallPlanCharts, error) {
	field := "reviewNvkeOperatorInstallPlanCharts"
	if retry {
		field = "recoverNvkeOperatorInstallPlanChartsPreparation"
	}
	return nvkeMutation[NVKEInstallPlanCharts](ctx, c.nvkeInstallPlanClient(), field, "NvkeOperatorInstallPlanChartsReviewInput", nvkeInstallPlanChartsFields, input)
}
