package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

const nvkeAgentFields = `id version siteId siteVersion endpoint instanceId bundleManifestSha256 isEnabled timeoutSeconds hasCredentials verifiedAt verificationCode deletedAt`

func (c *Client) nvkeAgentClient() *Client {
	selected := *c
	transport := *c.httpClient
	transport.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return errors.New("operator agent API redirects are refused")
	}
	selected.httpClient = &transport
	return &selected
}

type NVKEAgentAuthority struct {
	ID                   string  `json:"id"`
	Version              int     `json:"version"`
	SiteID               string  `json:"siteId"`
	SiteVersion          int     `json:"siteVersion"`
	Endpoint             string  `json:"endpoint"`
	InstanceID           string  `json:"instanceId"`
	BundleManifestSHA256 string  `json:"bundleManifestSha256"`
	IsEnabled            bool    `json:"isEnabled"`
	TimeoutSeconds       int     `json:"timeoutSeconds"`
	HasCredentials       bool    `json:"hasCredentials"`
	VerifiedAt           *string `json:"verifiedAt"`
	VerificationCode     string  `json:"verificationCode"`
	DeletedAt            *string `json:"deletedAt"`
}

type NVKEAgentReview struct {
	SiteID              string `json:"siteId"`
	ExpectedActorID     int    `json:"expectedActorId"`
	ExpectedSiteVersion int    `json:"expectedSiteVersion"`
	ExpectedVersion     int    `json:"expectedVersion"`
}

type NVKEAgentCredentials struct {
	CAPEM                string `json:"caPem,omitempty"`
	ClientCertificatePEM string `json:"clientCertificatePem,omitempty"`
	ClientKeyPEM         string `json:"clientKeyPem,omitempty"`
}

type NVKEAgentConfigureInput struct {
	NVKEAgentReview
	Endpoint             string                `json:"endpoint"`
	InstanceID           string                `json:"instanceId"`
	BundleManifestSHA256 string                `json:"bundleManifestSha256"`
	TimeoutSeconds       int                   `json:"timeoutSeconds"`
	Credentials          *NVKEAgentCredentials `json:"credentials,omitempty"`
}

type NVKEAgentOption struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Required bool     `json:"required"`
	Choices  []string `json:"choices"`
}
type NVKEAgentCommand struct {
	Key                  string            `json:"key"`
	Positionals          []string          `json:"positionals"`
	Options              []NVKEAgentOption `json:"options"`
	InfrastructureChange bool              `json:"infrastructureChange"`
}
type NVKEAgentCatalog struct {
	Revision string             `json:"revision"`
	Commands []NVKEAgentCommand `json:"commands"`
}

func (c *Client) NVKEAgentCatalog(ctx context.Context) (*NVKEAgentCatalog, error) {
	var response struct {
		Catalog *NVKEAgentCatalog `json:"astroliftNvkeOperatorContract"`
	}
	if err := c.nvkeAgentClient().GraphQL(ctx, `query{astroliftNvkeOperatorContract{revision commands{key positionals infrastructureChange options{name kind required choices}}}}`, nil, &response); err != nil || response.Catalog == nil || response.Catalog.Commands == nil {
		return nil, errors.New("NVKE operator agent catalog unavailable")
	}
	return response.Catalog, nil
}
func (c *Client) NVKEAgentAuthority(ctx context.Context, site string) (*NVKEAgentAuthority, error) {
	var response map[string]*NVKEAgentAuthority
	err := c.nvkeAgentClient().GraphQL(ctx, `query($site:GUID!){astroliftNvkeOperatorAuthority(siteId:$site){`+nvkeAgentFields+`}}`, map[string]any{"site": site}, &response)
	row, present := response["astroliftNvkeOperatorAuthority"]
	if err != nil || !present || (row != nil && row.SiteID != site) {
		return nil, errors.New("NVKE operator agent authority unavailable or mismatched")
	}
	return row, nil
}
func (c *Client) ConfigureNVKEAgentAuthority(ctx context.Context, input NVKEAgentConfigureInput) (*NVKEAgentAuthority, error) {
	return nvkeMutation[NVKEAgentAuthority](ctx, c.nvkeAgentClient(), "configureNvkeOperatorAuthority", "ConfigureNvkeOperatorAuthorityInput", nvkeAgentFields, input)
}
func (c *Client) VerifyNVKEAgentAuthority(ctx context.Context, input NVKEAgentReview) (*NVKEAgentAuthority, error) {
	return nvkeMutation[NVKEAgentAuthority](ctx, c.nvkeAgentClient(), "verifyNvkeOperatorAuthority", "NvkeOperatorAuthorityReviewInput", nvkeAgentFields, input)
}
func (c *Client) RemoveNVKEAgentAuthority(ctx context.Context, input NVKEAgentReview) (*NVKEAgentAuthority, error) {
	return nvkeMutation[NVKEAgentAuthority](ctx, c.nvkeAgentClient(), "removeNvkeOperatorAuthority", "NvkeOperatorAuthorityReviewInput", nvkeAgentFields, input)
}
func (c *Client) SetNVKEAgentAuthorityEnabled(ctx context.Context, input NVKEAgentReview, enabled bool) (*NVKEAgentAuthority, error) {
	return nvkeMutation[NVKEAgentAuthority](ctx, c.nvkeAgentClient(), "setNvkeOperatorAuthorityEnabled", "SetNvkeOperatorAuthorityEnabledInput", nvkeAgentFields, struct {
		NVKEAgentReview
		Enabled bool `json:"enabled"`
	}{input, enabled})
}

type NVKEAgentRequestInput struct {
	SiteID                          string `json:"siteId"`
	ExpectedActorID                 int    `json:"expectedActorId"`
	ExpectedSiteVersion             int    `json:"expectedSiteVersion"`
	ExpectedAuthorityVersion        int    `json:"expectedAuthorityVersion"`
	RequestID                       string `json:"requestId"`
	RequestText                     string `json:"requestText"`
	AcknowledgeInfrastructureChange bool   `json:"acknowledgeInfrastructureChange"`
	TimeoutSeconds                  int    `json:"timeoutSeconds"`
}

func (c *Client) RequestNVKEAgentJob(ctx context.Context, input NVKEAgentRequestInput) (*NVKEOperation, error) {
	return nvkeMutation[NVKEOperation](ctx, c.nvkeAgentClient(), "requestNvkeOperatorJob", "RequestNvkeOperatorJobInput", nvkeOperationFields, input)
}

type NVKEAgentJobReview struct {
	OperationID              string `json:"operationId"`
	ExpectedActorID          int    `json:"expectedActorId"`
	ExpectedSiteVersion      int    `json:"expectedSiteVersion"`
	ExpectedAuthorityVersion int    `json:"expectedAuthorityVersion"`
	ExpectedOperationVersion int    `json:"expectedOperationVersion"`
}

func (c *Client) ReconcileNVKEAgentJob(ctx context.Context, input NVKEAgentJobReview) (*NVKEOperation, error) {
	return nvkeMutation[NVKEOperation](ctx, c.nvkeAgentClient(), "reconcileNvkeOperatorJob", "NvkeOperatorJobReviewInput", nvkeOperationFields, input)
}

func (c *Client) StopNVKEAgentJob(ctx context.Context, input NVKEAgentJobReview) (*NVKEOperation, error) {
	return nvkeMutation[NVKEOperation](ctx, c.nvkeAgentClient(), "stopNvkeOperatorJob", "NvkeOperatorJobReviewInput", nvkeOperationFields, input)
}

type NVKEAgentArtifactInput struct {
	NVKEAgentJobReview
	Name   string `json:"name"`
	Offset string `json:"offset"`
	Limit  int    `json:"limit"`
}

type NVKEAgentArtifactChunk struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Offset     string `json:"offset"`
	NextOffset string `json:"nextOffset"`
	Size       string `json:"size"`
	SHA256     string `json:"sha256"`
	DataBase64 string `json:"dataBase64"`
}

func (c *Client) NVKEAgentArtifact(ctx context.Context, input NVKEAgentArtifactInput) (*NVKEAgentArtifactChunk, error) {
	var response map[string]nvkeResult[string]
	if err := c.nvkeAgentClient().GraphQL(ctx, `query($input:ReadNvkeOperatorArtifactInput!){astroliftNvkeOperatorArtifactText(input:$input){ok errors{code} data}}`, map[string]any{"input": input}, &response); err != nil {
		return nil, errors.New("NVKE operator artifact reply unavailable")
	}
	result, present := response["astroliftNvkeOperatorArtifactText"]
	if !present {
		return nil, errors.New("NVKE operator artifact reply incomplete")
	}
	text, err := result.value()
	if err != nil {
		return nil, err
	}
	var chunk NVKEAgentArtifactChunk
	if json.Unmarshal([]byte(*text), &chunk) != nil {
		return nil, errors.New("NVKE operator artifact encoding invalid")
	}
	return &chunk, nil
}
