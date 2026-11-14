package api

import "context"

type NVKEInstallPlanImageSource struct {
	WorkloadID              string `json:"workloadId,omitempty"`
	ExpectedWorkloadVersion int    `json:"expectedWorkloadVersion,omitempty"`
	WorkloadDigest          string `json:"workloadDigest,omitempty"`
	NVKEInstallPlanTreeSource
	TreeID              string `json:"treeId"`
	ExpectedTreeVersion int    `json:"expectedTreeVersion"`
	TreeDigest          string `json:"treeDigest"`
}

type NVKEInstallPlanImagePrepare struct {
	NVKEInstallPlanImageSource
	RequestText string `json:"requestText"`
}

type NVKEInstallPlanImageReview struct {
	NVKEInstallPlanImageSource
	ArtifactID string `json:"artifactId"`
}

type NVKEInstallPlanImage struct {
	WorkloadID             string `json:"workloadId"`
	WorkloadVersion        *int   `json:"workloadVersion"`
	WorkloadDigest         string `json:"workloadDigest"`
	ID                     string `json:"id"`
	Version                int    `json:"version"`
	Status                 string `json:"status"`
	SourceKind             string `json:"sourceKind"`
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
	RootDigest             string `json:"rootDigest"`
	BlobCount              *int   `json:"blobCount"`
	Bytes                  string `json:"bytes"`
	CaptureDigest          string `json:"captureDigest"`
	ObservedAt             string `json:"observedAt"`
	ExecutionState         string `json:"executionState"`
	ContinuationAuthorized *bool  `json:"continuationAuthorized"`
}

const nvkeInstallPlanImageFields = `id version status sourceKind workloadId workloadVersion workloadDigest planId planVersion chartArtifactId chartArtifactVersion chartArtifactDigest treeId treeVersion treeDigest operationId operationVersion siteId siteVersion authorityId authorityVersion planDigest artifactDigest sourceDigest captureDigest rootDigest blobCount bytes observedAt executionState continuationAuthorized`

func (c *Client) PrepareNVKEInstallPlanImage(ctx context.Context, input NVKEInstallPlanImagePrepare) (*NVKEInstallPlanImage, error) {
	return nvkeMutation[NVKEInstallPlanImage](ctx, c.nvkeInstallPlanClient(), "prepareNvkeOperatorInstallPlanImage", "PrepareNvkeOperatorInstallPlanImageInput", nvkeInstallPlanImageFields, input)
}

func (c *Client) ReviewNVKEInstallPlanImage(ctx context.Context, input NVKEInstallPlanImageReview, retry bool) (*NVKEInstallPlanImage, error) {
	field := "reviewNvkeOperatorInstallPlanImage"
	if retry {
		field = "recoverNvkeOperatorInstallPlanImagePreparation"
	}
	return nvkeMutation[NVKEInstallPlanImage](ctx, c.nvkeInstallPlanClient(), field, "NvkeOperatorInstallPlanImageReviewInput", nvkeInstallPlanImageFields, input)
}
