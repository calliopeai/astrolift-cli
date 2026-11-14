package api

import "context"

type NVKEInstallPlanVerificationSource struct {
	NVKEInstallPlanContextSource
	ContextID              string `json:"contextId"`
	ExpectedContextVersion int    `json:"expectedContextVersion"`
	ContextDigest          string `json:"contextDigest"`
}

type NVKEInstallPlanVerificationPrepare struct {
	NVKEInstallPlanVerificationSource
	RequestText string `json:"requestText"`
}

type NVKEInstallPlanVerificationReview struct {
	NVKEInstallPlanVerificationSource
	ArtifactID string `json:"artifactId"`
}

type NVKEInstallPlanVerification struct {
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
	ContextID              string `json:"contextId"`
	ContextVersion         int    `json:"contextVersion"`
	ArtifactDigest         string `json:"artifactDigest"`
	VerificationDigest     string `json:"verificationDigest"`
	Requests               *int   `json:"requests"`
	Bytes                  string `json:"bytes"`
	ObservedAt             string `json:"observedAt"`
	ExecutionState         string `json:"executionState"`
	ContinuationAuthorized *bool  `json:"continuationAuthorized"`
}

const nvkeInstallPlanVerificationFields = `id version status planId planVersion chartArtifactId chartArtifactVersion chartArtifactDigest treeId treeVersion treeDigest operationId operationVersion siteId siteVersion authorityId authorityVersion planDigest item ordinal captureDigest frozenChartSha256 contextDigest contextId contextVersion artifactDigest verificationDigest requests bytes observedAt executionState continuationAuthorized`

func (c *Client) PrepareNVKEInstallPlanVerification(ctx context.Context, input NVKEInstallPlanVerificationPrepare) (*NVKEInstallPlanVerification, error) {
	return nvkeMutation[NVKEInstallPlanVerification](ctx, c.nvkeInstallPlanClient(), "prepareNvkeOperatorInstallPlanVerification", "PrepareNvkeOperatorInstallPlanVerificationInput", nvkeInstallPlanVerificationFields, input)
}

func (c *Client) ReviewNVKEInstallPlanVerification(ctx context.Context, input NVKEInstallPlanVerificationReview, retry bool) (*NVKEInstallPlanVerification, error) {
	field := "reviewNvkeOperatorInstallPlanVerification"
	if retry {
		field = "recoverNvkeOperatorInstallPlanVerificationPreparation"
	}
	return nvkeMutation[NVKEInstallPlanVerification](ctx, c.nvkeInstallPlanClient(), field, "NvkeOperatorInstallPlanVerificationReviewInput", nvkeInstallPlanVerificationFields, input)
}
