package api

import "context"

type NVKEInstallPlanPullSecretAnalysisSource struct {
	NVKEInstallPlanImmutableSource
	PullAuthorityID              string `json:"pullAuthorityId"`
	ExpectedPullAuthorityVersion int    `json:"expectedPullAuthorityVersion"`
	PullAuthorityDigest          string `json:"pullAuthorityDigest"`
}

type NVKEInstallPlanPullSecretAnalysisPrepare struct {
	NVKEInstallPlanPullSecretAnalysisSource
	RequestText string `json:"requestText"`
}
type NVKEInstallPlanPullSecretAnalysisReview struct {
	NVKEInstallPlanPullSecretAnalysisSource
	ArtifactID string `json:"artifactId"`
}

type NVKEInstallPlanPullSecretAnalysis struct {
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
	AnalyzedAt             string `json:"analyzedAt"`
	SourceObservedAt       string `json:"sourceObservedAt"`
	ExecutionState         string `json:"executionState"`
	ContinuationAuthorized *bool  `json:"continuationAuthorized"`
	BindingID              string `json:"bindingId"`
	BindingVersion         int    `json:"bindingVersion"`
	AuthorityDigest        string `json:"authorityDigest"`
	ImageUseCount          *int   `json:"imageUseCount"`
	CandidateCount         *int   `json:"candidateCount"`
	PullAuthorityID        string `json:"pullAuthorityId"`
	PullAuthorityVersion   int    `json:"pullAuthorityVersion"`
	PullAuthorityDigest    string `json:"pullAuthorityDigest"`
	MissingReferenceCount  *int   `json:"missingReferenceCount"`
}

const nvkePullSecretAnalysisFields = `id version status planId planVersion chartArtifactId chartArtifactVersion chartArtifactDigest treeId treeVersion treeDigest operationId operationVersion siteId siteVersion authorityId authorityVersion planDigest artifactDigest sourceDigest captureDigest workloadId workloadVersion workloadDigest bindingId bindingVersion bindingDigest pullAuthorityId pullAuthorityVersion pullAuthorityDigest analyzedAt sourceObservedAt executionState continuationAuthorized authorityDigest imageUseCount candidateCount missingReferenceCount`

func (c *Client) PrepareNVKEInstallPlanPullSecretAnalysis(ctx context.Context, input NVKEInstallPlanPullSecretAnalysisPrepare) (*NVKEInstallPlanPullSecretAnalysis, error) {
	return nvkeMutation[NVKEInstallPlanPullSecretAnalysis](ctx, c.nvkeInstallPlanClient(), "prepareNvkeOperatorInstallPlanPullSecretAnalysis", "PrepareNvkeOperatorInstallPlanPullSecretAnalysisInput", nvkePullSecretAnalysisFields, input)
}

func (c *Client) ReviewNVKEInstallPlanPullSecretAnalysis(ctx context.Context, input NVKEInstallPlanPullSecretAnalysisReview, retry bool) (*NVKEInstallPlanPullSecretAnalysis, error) {
	field := "reviewNvkeOperatorInstallPlanPullSecretAnalysis"
	if retry {
		field = "recoverNvkeOperatorInstallPlanPullSecretAnalysisPreparation"
	}
	return nvkeMutation[NVKEInstallPlanPullSecretAnalysis](ctx, c.nvkeInstallPlanClient(), field, "NvkeOperatorInstallPlanPullSecretAnalysisReviewInput", nvkePullSecretAnalysisFields, input)
}
