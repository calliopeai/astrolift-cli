package api

import "context"

type NVKEInstallEvidence struct {
	GuardSHA256    string `json:"guardSha256"`
	StartedSHA256  string `json:"startedSha256"`
	FinishedSHA256 string `json:"finishedSha256"`
	StdoutSHA256   string `json:"stdoutSha256"`
	StderrSHA256   string `json:"stderrSha256"`
}

type NVKEInstallWorkload struct {
	Namespace          string  `json:"namespace"`
	Kind               string  `json:"kind"`
	Name               string  `json:"name"`
	Generation         string  `json:"generation"`
	ObservedGeneration *string `json:"observedGeneration"`
	DesiredReplicas    string  `json:"desiredReplicas"`
	ReadyReplicas      *string `json:"readyReplicas"`
	UpdatedReplicas    *string `json:"updatedReplicas"`
	Observation        string  `json:"observation"`
}

type NVKEInstallReview struct {
	SiteID                    string                `json:"siteId"`
	SiteVersion               int                   `json:"siteVersion"`
	OperationID               string                `json:"operationId"`
	OperationVersion          int                   `json:"operationVersion"`
	AuthorityID               string                `json:"authorityId"`
	AuthorityVersion          int                   `json:"authorityVersion"`
	InstanceID                string                `json:"instanceId"`
	BundleManifestSHA256      string                `json:"bundleManifestSha256"`
	NativeRevision            string                `json:"nativeRevision"`
	NativeRunID               string                `json:"nativeRunId"`
	ReviewDigest              string                `json:"reviewDigest"`
	ObservedAt                string                `json:"observedAt"`
	OriginalStartedAt         string                `json:"originalStartedAt"`
	NativeArgumentsSHA256     string                `json:"nativeArgumentsSha256"`
	NativeExitCode            int                   `json:"nativeExitCode"`
	APIReady                  *bool                 `json:"apiReady"`
	ContinuationAuthorized    *bool                 `json:"continuationAuthorized"`
	ReviewerSHA256            string                `json:"reviewerSha256"`
	QualificationHelperSHA256 string                `json:"qualificationHelperSha256"`
	Evidence                  NVKEInstallEvidence   `json:"evidence"`
	Workloads                 []NVKEInstallWorkload `json:"workloads"`
}

const nvkeInstallReviewFields = `siteId siteVersion operationId operationVersion authorityId authorityVersion instanceId bundleManifestSha256 nativeRevision nativeRunId reviewDigest observedAt originalStartedAt nativeArgumentsSha256 nativeExitCode apiReady continuationAuthorized reviewerSha256 qualificationHelperSha256 evidence { guardSha256 startedSha256 finishedSha256 stdoutSha256 stderrSha256 } workloads { namespace kind name generation observedGeneration desiredReplicas readyReplicas updatedReplicas observation }`

func (c *Client) ReviewNVKEAgentInstall(ctx context.Context, input NVKEAgentJobReview) (*NVKEInstallReview, error) {
	return nvkeMutation[NVKEInstallReview](ctx, c.nvkeAgentClient(), "reviewNvkeOperatorInstall", "NvkeOperatorJobReviewInput", nvkeInstallReviewFields, input)
}
