package api

import "context"

type NVKEInstallPlanNodePlatformsPrepare = NVKEInstallPlanImmutablePrepare
type NVKEInstallPlanNodePlatformsReview = NVKEInstallPlanImmutableReview

type NVKEInstallPlanNodePlatforms struct {
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
	CaptureDigest          string `json:"captureDigest"`
	ObservedAt             string `json:"observedAt"`
	ExecutionState         string `json:"executionState"`
	ContinuationAuthorized *bool  `json:"continuationAuthorized"`
	BindingID              string `json:"bindingId"`
	BindingVersion         int    `json:"bindingVersion"`
	PlatformDigest         string `json:"platformDigest"`
	NodeCount              *int   `json:"nodeCount"`
	ContentCount           *int   `json:"contentCount"`
	ImageReferenceCount    *int   `json:"imageReferenceCount"`
	PairCount              *int   `json:"pairCount"`
	IssueCount             *int   `json:"issueCount"`
}

const nvkeNodePlatformsFields = `id version status planId planVersion chartArtifactId chartArtifactVersion chartArtifactDigest treeId treeVersion treeDigest operationId operationVersion siteId siteVersion authorityId authorityVersion planDigest artifactDigest sourceDigest captureDigest workloadId workloadVersion workloadDigest bindingId bindingVersion bindingDigest observedAt executionState continuationAuthorized platformDigest nodeCount contentCount imageReferenceCount pairCount issueCount`

func (c *Client) PrepareNVKEInstallPlanNodePlatforms(ctx context.Context, input NVKEInstallPlanNodePlatformsPrepare) (*NVKEInstallPlanNodePlatforms, error) {
	return nvkeMutation[NVKEInstallPlanNodePlatforms](ctx, c.nvkeInstallPlanClient(), "prepareNvkeOperatorInstallPlanNodePlatforms", "PrepareNvkeOperatorInstallPlanNodePlatformsInput", nvkeNodePlatformsFields, input)
}

func (c *Client) ReviewNVKEInstallPlanNodePlatforms(ctx context.Context, input NVKEInstallPlanNodePlatformsReview, retry bool) (*NVKEInstallPlanNodePlatforms, error) {
	field := "reviewNvkeOperatorInstallPlanNodePlatforms"
	if retry {
		field = "recoverNvkeOperatorInstallPlanNodePlatformsPreparation"
	}
	return nvkeMutation[NVKEInstallPlanNodePlatforms](ctx, c.nvkeInstallPlanClient(), field, "NvkeOperatorInstallPlanNodePlatformsReviewInput", nvkeNodePlatformsFields, input)
}
