package api

import "context"

type NVKEInstallPlanContextSource struct {
	NVKEInstallPlanTreeSource
	TreeID              string `json:"treeId"`
	ExpectedTreeVersion int    `json:"expectedTreeVersion"`
	TreeDigest          string `json:"treeDigest"`
	Item                string `json:"item"`
	Ordinal             int    `json:"ordinal"`
}

type NVKEInstallPlanContextPrepare struct {
	NVKEInstallPlanContextSource
	RequestText string `json:"requestText"`
}

type NVKEInstallPlanContextReview struct {
	NVKEInstallPlanContextSource
	ArtifactID string `json:"artifactId"`
}

type NVKEInstallPlanContext struct {
	ID                     string `json:"id"`
	Version                int    `json:"version"`
	Status                 string `json:"status"`
	PlanID                 string `json:"planId"`
	PlanVersion            int    `json:"planVersion"`
	ChartArtifactID        string `json:"chartArtifactId"`
	ChartArtifactVersion   int    `json:"chartArtifactVersion"`
	ChartArtifactDigest    string `json:"chartArtifactDigest"`
	TreeID                 string `json:"treeId"`
	TreeVersion            int    `json:"treeVersion"`
	TreeDigest             string `json:"treeDigest"`
	OperationID            string `json:"operationId"`
	OperationVersion       int    `json:"operationVersion"`
	SiteID                 string `json:"siteId"`
	SiteVersion            int    `json:"siteVersion"`
	AuthorityID            string `json:"authorityId"`
	AuthorityVersion       int    `json:"authorityVersion"`
	PlanDigest             string `json:"planDigest"`
	Item                   string `json:"item"`
	Ordinal                *int   `json:"ordinal"`
	CaptureDigest          string `json:"captureDigest"`
	FrozenChartSHA256      string `json:"frozenChartSha256"`
	ContextDigest          string `json:"contextDigest"`
	DocumentCount          *int   `json:"documentCount"`
	LookupCount            *int   `json:"lookupCount"`
	ObservedAt             string `json:"observedAt"`
	ExecutionState         string `json:"executionState"`
	ContinuationAuthorized *bool  `json:"continuationAuthorized"`
}

const nvkeInstallPlanContextFields = `id version status planId planVersion chartArtifactId chartArtifactVersion chartArtifactDigest treeId treeVersion treeDigest operationId operationVersion siteId siteVersion authorityId authorityVersion planDigest item ordinal captureDigest frozenChartSha256 contextDigest documentCount lookupCount observedAt executionState continuationAuthorized`

func (c *Client) PrepareNVKEInstallPlanContext(ctx context.Context, input NVKEInstallPlanContextPrepare) (*NVKEInstallPlanContext, error) {
	return nvkeMutation[NVKEInstallPlanContext](ctx, c.nvkeInstallPlanClient(), "prepareNvkeOperatorInstallPlanContext", "PrepareNvkeOperatorInstallPlanContextInput", nvkeInstallPlanContextFields, input)
}

func (c *Client) ReviewNVKEInstallPlanContext(ctx context.Context, input NVKEInstallPlanContextReview, retry bool) (*NVKEInstallPlanContext, error) {
	field := "reviewNvkeOperatorInstallPlanContext"
	if retry {
		field = "recoverNvkeOperatorInstallPlanContextPreparation"
	}
	return nvkeMutation[NVKEInstallPlanContext](ctx, c.nvkeInstallPlanClient(), field, "NvkeOperatorInstallPlanContextReviewInput", nvkeInstallPlanContextFields, input)
}
