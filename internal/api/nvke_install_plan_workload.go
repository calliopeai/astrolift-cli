package api

import "context"

type NVKEInstallPlanWorkloadSource struct {
	NVKEInstallPlanTreeSource
	TreeID              string `json:"treeId"`
	ExpectedTreeVersion int    `json:"expectedTreeVersion"`
	TreeDigest          string `json:"treeDigest"`
}

type NVKEInstallPlanWorkloadPrepare struct {
	NVKEInstallPlanWorkloadSource
	RequestText string `json:"requestText"`
}

type NVKEInstallPlanWorkloadReview struct {
	NVKEInstallPlanWorkloadSource
	ArtifactID string `json:"artifactId"`
}

type NVKEInstallPlanWorkload struct {
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
	ArtifactDigest         string `json:"artifactDigest"`
	SourceDigest           string `json:"sourceDigest"`
	WorkloadDigest         string `json:"workloadDigest"`
	ItemCount              *int   `json:"itemCount"`
	ResourceCount          *int   `json:"resourceCount"`
	ImageUseCount          *int   `json:"imageUseCount"`
	ImageReferenceCount    *int   `json:"imageReferenceCount"`
	IssueCount             *int   `json:"issueCount"`
	CaptureDigest          string `json:"captureDigest"`
	ObservedAt             string `json:"observedAt"`
	ExecutionState         string `json:"executionState"`
	ContinuationAuthorized *bool  `json:"continuationAuthorized"`
}

const nvkeInstallPlanWorkloadFields = `id version status planId planVersion chartArtifactId chartArtifactVersion chartArtifactDigest treeId treeVersion treeDigest operationId operationVersion siteId siteVersion authorityId authorityVersion planDigest artifactDigest sourceDigest captureDigest workloadDigest itemCount resourceCount imageUseCount imageReferenceCount issueCount observedAt executionState continuationAuthorized`

func (c *Client) PrepareNVKEInstallPlanWorkload(ctx context.Context, input NVKEInstallPlanWorkloadPrepare) (*NVKEInstallPlanWorkload, error) {
	return nvkeMutation[NVKEInstallPlanWorkload](ctx, c.nvkeInstallPlanClient(), "prepareNvkeOperatorInstallPlanWorkload", "PrepareNvkeOperatorInstallPlanWorkloadInput", nvkeInstallPlanWorkloadFields, input)
}

func (c *Client) ReviewNVKEInstallPlanWorkload(ctx context.Context, input NVKEInstallPlanWorkloadReview, retry bool) (*NVKEInstallPlanWorkload, error) {
	field := "reviewNvkeOperatorInstallPlanWorkload"
	if retry {
		field = "recoverNvkeOperatorInstallPlanWorkloadPreparation"
	}
	return nvkeMutation[NVKEInstallPlanWorkload](ctx, c.nvkeInstallPlanClient(), field, "NvkeOperatorInstallPlanWorkloadReviewInput", nvkeInstallPlanWorkloadFields, input)
}
