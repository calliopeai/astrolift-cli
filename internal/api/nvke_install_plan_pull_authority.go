package api

import "context"

type NVKEInstallPlanPullAuthorityPrepare = NVKEInstallPlanImmutablePrepare
type NVKEInstallPlanPullAuthorityReview = NVKEInstallPlanImmutableReview

type NVKEInstallPlanPullAuthority struct {
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
	AuthorityDigest        string `json:"authorityDigest"`
	PodCount               *int   `json:"podCount"`
	ServiceAccountCount    *int   `json:"serviceAccountCount"`
	SecretCount            *int   `json:"secretCount"`
	MissingReferenceCount  *int   `json:"missingReferenceCount"`
}

const nvkePullAuthorityFields = `id version status planId planVersion chartArtifactId chartArtifactVersion chartArtifactDigest treeId treeVersion treeDigest operationId operationVersion siteId siteVersion authorityId authorityVersion planDigest artifactDigest sourceDigest captureDigest workloadId workloadVersion workloadDigest bindingId bindingVersion bindingDigest observedAt executionState continuationAuthorized authorityDigest podCount serviceAccountCount secretCount missingReferenceCount`

func (c *Client) PrepareNVKEInstallPlanPullAuthority(ctx context.Context, input NVKEInstallPlanPullAuthorityPrepare) (*NVKEInstallPlanPullAuthority, error) {
	return nvkeMutation[NVKEInstallPlanPullAuthority](ctx, c.nvkeInstallPlanClient(), "prepareNvkeOperatorInstallPlanPullAuthority", "PrepareNvkeOperatorInstallPlanPullAuthorityInput", nvkePullAuthorityFields, input)
}

func (c *Client) ReviewNVKEInstallPlanPullAuthority(ctx context.Context, input NVKEInstallPlanPullAuthorityReview, retry bool) (*NVKEInstallPlanPullAuthority, error) {
	field := "reviewNvkeOperatorInstallPlanPullAuthority"
	if retry {
		field = "recoverNvkeOperatorInstallPlanPullAuthorityPreparation"
	}
	return nvkeMutation[NVKEInstallPlanPullAuthority](ctx, c.nvkeInstallPlanClient(), field, "NvkeOperatorInstallPlanPullAuthorityReviewInput", nvkePullAuthorityFields, input)
}
