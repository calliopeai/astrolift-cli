package api

import "context"

type NVKEInstallPlanTreeSource struct {
	NVKEInstallPlanChartsSource
	ChartArtifactID              string `json:"chartArtifactId"`
	ExpectedChartArtifactVersion int    `json:"expectedChartArtifactVersion"`
	ChartArtifactDigest          string `json:"chartArtifactDigest"`
}

type NVKEInstallPlanTreePrepare struct {
	NVKEInstallPlanTreeSource
	RequestText string `json:"requestText"`
}

type NVKEInstallPlanTreeReview struct {
	NVKEInstallPlanTreeSource
	ArtifactID string `json:"artifactId"`
}

type NVKEInstallPlanTree struct {
	ID                     string `json:"id"`
	Version                int    `json:"version"`
	Status                 string `json:"status"`
	PlanID                 string `json:"planId"`
	PlanVersion            int    `json:"planVersion"`
	ChartArtifactID        string `json:"chartArtifactId"`
	ChartArtifactVersion   int    `json:"chartArtifactVersion"`
	ChartArtifactDigest    string `json:"chartArtifactDigest"`
	OperationID            string `json:"operationId"`
	OperationVersion       int    `json:"operationVersion"`
	SiteID                 string `json:"siteId"`
	SiteVersion            int    `json:"siteVersion"`
	AuthorityID            string `json:"authorityId"`
	AuthorityVersion       int    `json:"authorityVersion"`
	PlanDigest             string `json:"planDigest"`
	TreeDigest             string `json:"treeDigest"`
	FileCount              *int   `json:"fileCount"`
	Bytes                  string `json:"bytes"`
	ExecutionState         string `json:"executionState"`
	ContinuationAuthorized *bool  `json:"continuationAuthorized"`
}

const nvkeInstallPlanTreeFields = `id version status planId planVersion chartArtifactId chartArtifactVersion chartArtifactDigest operationId operationVersion siteId siteVersion authorityId authorityVersion planDigest treeDigest fileCount bytes executionState continuationAuthorized`

func (c *Client) PrepareNVKEInstallPlanTree(ctx context.Context, input NVKEInstallPlanTreePrepare) (*NVKEInstallPlanTree, error) {
	return nvkeMutation[NVKEInstallPlanTree](ctx, c.nvkeInstallPlanClient(), "prepareNvkeOperatorInstallPlanTree", "PrepareNvkeOperatorInstallPlanTreeInput", nvkeInstallPlanTreeFields, input)
}

func (c *Client) ReviewNVKEInstallPlanTree(ctx context.Context, input NVKEInstallPlanTreeReview, retry bool) (*NVKEInstallPlanTree, error) {
	field := "reviewNvkeOperatorInstallPlanTree"
	if retry {
		field = "recoverNvkeOperatorInstallPlanTreePreparation"
	}
	return nvkeMutation[NVKEInstallPlanTree](ctx, c.nvkeInstallPlanClient(), field, "NvkeOperatorInstallPlanTreeReviewInput", nvkeInstallPlanTreeFields, input)
}
