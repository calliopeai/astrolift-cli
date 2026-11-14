package api

import "context"

type NVKEInstallPlanImageBindingSource struct {
	NVKEInstallPlanWorkloadSource
	WorkloadID              string `json:"workloadId"`
	ExpectedWorkloadVersion int    `json:"expectedWorkloadVersion"`
	WorkloadDigest          string `json:"workloadDigest"`
}

type NVKEInstallPlanImageBindingPrepare struct {
	NVKEInstallPlanImageBindingSource
	RequestText string `json:"requestText"`
}

type NVKEInstallPlanImageBindingReview struct {
	NVKEInstallPlanImageBindingSource
	ArtifactID string `json:"artifactId"`
}

type NVKEInstallPlanImageBinding struct {
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
	WorkloadID             string `json:"workloadId"`
	WorkloadVersion        int    `json:"workloadVersion"`
	BindingDigest          string `json:"bindingDigest"`
	ImageCount             *int   `json:"imageCount"`
	ImageUseCount          *int   `json:"imageUseCount"`
	ImportCount            *int   `json:"importCount"`
	RewriteCount           *int   `json:"rewriteCount"`
	VerifiedImageBytes     string `json:"verifiedImageBytes"`
	WorkloadIssueCount     *int   `json:"workloadIssueCount"`
	CaptureDigest          string `json:"captureDigest"`
	ObservedAt             string `json:"observedAt"`
	ExecutionState         string `json:"executionState"`
	ContinuationAuthorized *bool  `json:"continuationAuthorized"`
}

const nvkeInstallPlanImageBindingFields = `id version status planId planVersion chartArtifactId chartArtifactVersion chartArtifactDigest treeId treeVersion treeDigest operationId operationVersion siteId siteVersion authorityId authorityVersion planDigest artifactDigest sourceDigest captureDigest workloadId workloadVersion workloadDigest bindingDigest imageCount imageUseCount importCount rewriteCount verifiedImageBytes workloadIssueCount observedAt executionState continuationAuthorized`

func (c *Client) PrepareNVKEInstallPlanImageBinding(ctx context.Context, input NVKEInstallPlanImageBindingPrepare) (*NVKEInstallPlanImageBinding, error) {
	return nvkeMutation[NVKEInstallPlanImageBinding](ctx, c.nvkeInstallPlanClient(), "prepareNvkeOperatorInstallPlanImageBinding", "PrepareNvkeOperatorInstallPlanImageBindingInput", nvkeInstallPlanImageBindingFields, input)
}

func (c *Client) ReviewNVKEInstallPlanImageBinding(ctx context.Context, input NVKEInstallPlanImageBindingReview, retry bool) (*NVKEInstallPlanImageBinding, error) {
	field := "reviewNvkeOperatorInstallPlanImageBinding"
	if retry {
		field = "recoverNvkeOperatorInstallPlanImageBindingPreparation"
	}
	return nvkeMutation[NVKEInstallPlanImageBinding](ctx, c.nvkeInstallPlanClient(), field, "NvkeOperatorInstallPlanImageBindingReviewInput", nvkeInstallPlanImageBindingFields, input)
}
