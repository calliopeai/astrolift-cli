package api

import (
	"context"
	"errors"
	"time"
)

type NVKEInstallPlanSource struct {
	SiteID                     string `json:"siteId"`
	InstanceID                 string `json:"instanceId"`
	JobID                      string `json:"jobId"`
	NativeRunID                string `json:"nativeRunId"`
	NativeRevision             string `json:"nativeRevision"`
	NativeExitCode             int    `json:"nativeExitCode"`
	BundleManifestSHA256       string `json:"bundleManifestSha256"`
	NativeArgumentsSHA256      string `json:"nativeArgumentsSha256"`
	RetainedInputsSHA256       string `json:"retainedInputsSha256"`
	ProposedInputsSHA256       string `json:"proposedInputsSha256"`
	ProposedExecutionSHA256    string `json:"proposedExecutionSha256"`
	PrivateSourceSHA256        string `json:"privateSourceSha256"`
	ReviewDigest               string `json:"reviewDigest"`
	StderrSHA256               string `json:"stderrSha256"`
	OriginalStartedAt          string `json:"originalStartedAt"`
	ReviewObservedAt           string `json:"reviewObservedAt"`
	OriginalDeploymentEvidence string `json:"originalDeploymentEvidence"`
}
type NVKEInstallPlanHeader struct {
	Format                             int                   `json:"format"`
	Scope                              string                `json:"scope"`
	ID                                 string                `json:"id"`
	PreparedAt                         string                `json:"preparedAt"`
	Source                             NVKEInstallPlanSource `json:"source"`
	PhaseEvidence                      string                `json:"phaseEvidence"`
	RequiresOriginalSourceVerification *bool                 `json:"requiresOriginalSourceVerification"`
	RequiresExplicitAcknowledgement    *bool                 `json:"requiresExplicitAcknowledgement"`
	ContinuationAuthorized             *bool                 `json:"continuationAuthorized"`
	FirstRemainingOrdinal              *int                  `json:"firstRemainingOrdinal"`
	PlanDigest                         string                `json:"planDigest"`
}
type NVKEInstallPlanInfrastructure struct {
	APIReady      *bool  `json:"apiReady"`
	ObservedAt    string `json:"observedAt"`
	ClusterUID    string `json:"clusterUid"`
	WorkloadCount *int   `json:"workloadCount"`
}
type NVKEInstallPlan struct {
	ID               string                        `json:"id"`
	Version          int                           `json:"version"`
	Status           string                        `json:"status"`
	OperationID      string                        `json:"operationId"`
	OperationVersion int                           `json:"operationVersion"`
	SiteID           string                        `json:"siteId"`
	SiteVersion      int                           `json:"siteVersion"`
	AuthorityID      string                        `json:"authorityId"`
	AuthorityVersion int                           `json:"authorityVersion"`
	ItemCount        int                           `json:"itemCount"`
	RetainedBytes    string                        `json:"retainedBytes"`
	ProposedBytes    string                        `json:"proposedBytes"`
	Plan             NVKEInstallPlanHeader         `json:"plan"`
	Infrastructure   NVKEInstallPlanInfrastructure `json:"infrastructure"`
}
type NVKEInstallPlanItem struct {
	Name                  string `json:"name"`
	Type                  string `json:"type"`
	OriginalType          string `json:"originalType"`
	ContentSHA256         string `json:"contentSha256"`
	OriginalContentSHA256 string `json:"originalContentSha256"`
	Ordinal               *int   `json:"ordinal"`
	OriginalObservation   string `json:"originalObservation"`
	ProposedAction        string `json:"proposedAction"`
}
type NVKEInstallPlanReview struct {
	NVKEAgentJobReview
	PlanID string `json:"planId"`
}
type NVKEInstallPlanPrepare struct {
	NVKEAgentJobReview
	RequestText string `json:"requestText"`
}
type NVKEInstallPlanPage[T any] struct {
	PlanID     string  `json:"planId"`
	PlanDigest string  `json:"planDigest"`
	View       string  `json:"view"`
	Items      []T     `json:"items"`
	NextCursor *string `json:"nextCursor"`
	TotalCount *int    `json:"totalCount"`
}

const nvkeInstallPlanFields = `id version status operationId operationVersion siteId siteVersion authorityId authorityVersion itemCount retainedBytes proposedBytes plan {format scope id preparedAt phaseEvidence requiresOriginalSourceVerification requiresExplicitAcknowledgement continuationAuthorized firstRemainingOrdinal planDigest source {siteId instanceId jobId nativeRunId nativeRevision nativeExitCode bundleManifestSha256 nativeArgumentsSha256 retainedInputsSha256 proposedInputsSha256 proposedExecutionSha256 privateSourceSha256 reviewDigest stderrSha256 originalStartedAt reviewObservedAt originalDeploymentEvidence}} infrastructure {apiReady observedAt clusterUid workloadCount}`

func (c *Client) nvkeInstallPlanClient() *Client {
	selected := c.nvkeAgentClient()
	selected.SetTimeout(360 * time.Second)
	return selected
}

func (c *Client) PrepareNVKEInstallPlan(ctx context.Context, input NVKEInstallPlanPrepare) (*NVKEInstallPlan, error) {
	return nvkeMutation[NVKEInstallPlan](ctx, c.nvkeInstallPlanClient(), "prepareNvkeOperatorInstallPlan", "PrepareNvkeOperatorInstallPlanInput", nvkeInstallPlanFields, input)
}
func (c *Client) ReviewNVKEInstallPlan(ctx context.Context, input NVKEInstallPlanReview, retry bool) (*NVKEInstallPlan, error) {
	field := "reviewNvkeOperatorInstallPlan"
	if retry {
		field = "recoverNvkeOperatorInstallPlanPreparation"
	}
	return nvkeMutation[NVKEInstallPlan](ctx, c.nvkeInstallPlanClient(), field, "NvkeOperatorInstallPlanReviewInput", nvkeInstallPlanFields, input)
}
func nvkeInstallPlanPage[T any](ctx context.Context, c *Client, field, fields string, input NVKEInstallPlanReview, digest string, after *string, limit int, search string) (*NVKEInstallPlanPage[T], error) {
	var response map[string]nvkeResult[NVKEInstallPlanPage[T]]
	query := `query($input:NvkeOperatorInstallPlanReviewInput!,$digest:String!,$after:String,$limit:Int!,$search:String!){` + field + `(input:$input,planDigest:$digest,after:$after,limit:$limit,search:$search){ok errors{code} data{planId planDigest view nextCursor totalCount items{` + fields + `}}}}`
	err := c.nvkeInstallPlanClient().GraphQL(ctx, query, map[string]any{"input": input, "digest": digest, "after": after, "limit": limit, "search": search}, &response)
	value, ok := response[field]
	if err != nil || !ok {
		return nil, errors.New("NVKE install plan page unavailable; retain the reviewed plan identity")
	}
	return value.value()
}
func (c *Client) NVKEInstallPlanItems(ctx context.Context, input NVKEInstallPlanReview, digest string, after *string, limit int, search string) (*NVKEInstallPlanPage[NVKEInstallPlanItem], error) {
	return nvkeInstallPlanPage[NVKEInstallPlanItem](ctx, c, "astroliftNvkeOperatorInstallPlanItemPage", `name type originalType contentSha256 originalContentSha256 ordinal originalObservation proposedAction`, input, digest, after, limit, search)
}
func (c *Client) NVKEInstallPlanWorkloads(ctx context.Context, input NVKEInstallPlanReview, digest string, after *string, limit int, search string) (*NVKEInstallPlanPage[NVKEInstallWorkload], error) {
	return nvkeInstallPlanPage[NVKEInstallWorkload](ctx, c, "astroliftNvkeOperatorInstallPlanWorkloadPage", `namespace kind name generation observedGeneration desiredReplicas readyReplicas updatedReplicas observation`, input, digest, after, limit, search)
}
