package api

import "context"

type NVKEInstallPlanImmutableSource struct {
	NVKEInstallPlanImageBindingSource
	BindingID              string `json:"bindingId"`
	ExpectedBindingVersion int    `json:"expectedBindingVersion"`
	BindingDigest          string `json:"bindingDigest"`
}

type NVKEInstallPlanImmutablePrepare struct {
	NVKEInstallPlanImmutableSource
	RequestText string `json:"requestText"`
}

type NVKEInstallPlanImmutableReview struct {
	NVKEInstallPlanImmutableSource
	ArtifactID string `json:"artifactId"`
}

type NVKEInstallPlanImmutableArtifact struct {
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
	SourceObservedAt       string `json:"sourceObservedAt"`
	FileCount              *int   `json:"fileCount"`
	PayloadDigest          string `json:"payloadDigest,omitempty"`
	PayloadBytes           string `json:"payloadBytes,omitempty"`
	ResourceCount          *int   `json:"resourceCount,omitempty"`
	ImageUseCount          *int   `json:"imageUseCount,omitempty"`
	RewriteCount           *int   `json:"rewriteCount,omitempty"`
	ExecutionDigest        string `json:"executionDigest,omitempty"`
	Bytes                  string `json:"bytes,omitempty"`
	PhaseCount             *int   `json:"phaseCount,omitempty"`
	SkippedPhaseCount      *int   `json:"skippedPhaseCount,omitempty"`
	ImportCount            *int   `json:"importCount,omitempty"`
}

const nvkeImmutableFields = `id version status planId planVersion chartArtifactId chartArtifactVersion chartArtifactDigest treeId treeVersion treeDigest operationId operationVersion siteId siteVersion authorityId authorityVersion planDigest artifactDigest sourceDigest captureDigest workloadId workloadVersion workloadDigest bindingId bindingVersion bindingDigest fileCount observedAt sourceObservedAt executionState continuationAuthorized`

func nvkeImmutableSelection(tree bool) (string, string) {
	if tree {
		return "ImmutableExecutionTree", nvkeImmutableFields + ` executionDigest bytes phaseCount skippedPhaseCount importCount`
	}
	return "ImagePayload", nvkeImmutableFields + ` payloadDigest payloadBytes resourceCount imageUseCount rewriteCount`
}

func (c *Client) PrepareNVKEInstallPlanImmutable(ctx context.Context, input NVKEInstallPlanImmutablePrepare, tree bool) (*NVKEInstallPlanImmutableArtifact, error) {
	suffix, fields := nvkeImmutableSelection(tree)
	return nvkeMutation[NVKEInstallPlanImmutableArtifact](ctx, c.nvkeInstallPlanClient(), "prepareNvkeOperatorInstallPlan"+suffix, "PrepareNvkeOperatorInstallPlanImmutableInput", fields, input)
}

func (c *Client) ReviewNVKEInstallPlanImmutable(ctx context.Context, input NVKEInstallPlanImmutableReview, tree, retry bool) (*NVKEInstallPlanImmutableArtifact, error) {
	suffix, fields := nvkeImmutableSelection(tree)
	field := "reviewNvkeOperatorInstallPlan" + suffix
	if retry {
		field = "recoverNvkeOperatorInstallPlan" + suffix + "Preparation"
	}
	return nvkeMutation[NVKEInstallPlanImmutableArtifact](ctx, c.nvkeInstallPlanClient(), field, "NvkeOperatorInstallPlanImmutableReviewInput", fields, input)
}
