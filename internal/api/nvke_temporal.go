package api

import (
	"context"
	"encoding/json"
	"errors"
)

const nvkeTemporalFields = `id version siteId siteVersion address namespace taskQueue serverName useTls isEnabled timeoutSeconds hasCredentials verifiedAt verificationCode deletedAt`

type NVKENativeWorkflow struct {
	Name        string          `json:"name"`
	InputType   string          `json:"inputType"`
	Source      string          `json:"source"`
	Permission  string          `json:"permission"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type NVKETemporalAuthority struct {
	ID               string  `json:"id"`
	Version          int     `json:"version"`
	SiteID           string  `json:"siteId"`
	SiteVersion      int     `json:"siteVersion"`
	Address          string  `json:"address"`
	Namespace        string  `json:"namespace"`
	TaskQueue        string  `json:"taskQueue"`
	ServerName       string  `json:"serverName"`
	UseTLS           bool    `json:"useTls"`
	IsEnabled        bool    `json:"isEnabled"`
	TimeoutSeconds   int     `json:"timeoutSeconds"`
	HasCredentials   bool    `json:"hasCredentials"`
	VerifiedAt       *string `json:"verifiedAt"`
	VerificationCode string  `json:"verificationCode"`
	DeletedAt        *string `json:"deletedAt"`
}

type NVKETemporalReview struct {
	SiteID              string `json:"siteId"`
	ExpectedActorID     int    `json:"expectedActorId"`
	ExpectedSiteVersion int    `json:"expectedSiteVersion"`
	ExpectedVersion     int    `json:"expectedVersion"`
}

type NVKETemporalCredentials struct {
	APIKey               string `json:"apiKey,omitempty"`
	CAPEM                string `json:"caPem,omitempty"`
	ClientCertificatePEM string `json:"clientCertificatePem,omitempty"`
	ClientKeyPEM         string `json:"clientKeyPem,omitempty"`
}

type NVKETemporalConfigureInput struct {
	NVKETemporalReview
	Address        string                   `json:"address"`
	Namespace      string                   `json:"namespace"`
	TaskQueue      string                   `json:"taskQueue"`
	ServerName     string                   `json:"serverName"`
	UseTLS         bool                     `json:"useTls"`
	TimeoutSeconds int                      `json:"timeoutSeconds"`
	Credentials    *NVKETemporalCredentials `json:"credentials,omitempty"`
}

func (c *Client) NVKETemporalAuthority(ctx context.Context, site string) (*NVKETemporalAuthority, error) {
	var response map[string]*NVKETemporalAuthority
	err := c.GraphQL(ctx, `query($site:GUID!){astroliftNvkeTemporalAuthority(siteId:$site){`+nvkeTemporalFields+`}}`, map[string]interface{}{"site": site}, &response)
	if err != nil {
		return nil, errors.New("native Temporal authority metadata unavailable")
	}
	authority, present := response["astroliftNvkeTemporalAuthority"]
	if !present {
		return nil, errors.New("native Temporal authority metadata reply incomplete")
	}
	if authority != nil && authority.SiteID != site {
		return nil, errors.New("native Temporal authority reply did not match the site")
	}
	return authority, nil
}

func (c *Client) ConfigureNVKETemporalAuthority(ctx context.Context, input NVKETemporalConfigureInput) (*NVKETemporalAuthority, error) {
	return nvkeMutation[NVKETemporalAuthority](ctx, c, "configureNvkeTemporalAuthority", "ConfigureNvkeTemporalAuthorityInput", nvkeTemporalFields, input)
}

func (c *Client) VerifyNVKETemporalAuthority(ctx context.Context, input NVKETemporalReview) (*NVKETemporalAuthority, error) {
	return nvkeMutation[NVKETemporalAuthority](ctx, c, "verifyNvkeTemporalAuthority", "NvkeTemporalAuthorityReviewInput", nvkeTemporalFields, input)
}

func (c *Client) RemoveNVKETemporalAuthority(ctx context.Context, input NVKETemporalReview) (*NVKETemporalAuthority, error) {
	return nvkeMutation[NVKETemporalAuthority](ctx, c, "removeNvkeTemporalAuthority", "NvkeTemporalAuthorityReviewInput", nvkeTemporalFields, input)
}

func (c *Client) SetNVKETemporalAuthorityEnabled(ctx context.Context, input NVKETemporalReview, enabled bool) (*NVKETemporalAuthority, error) {
	return nvkeMutation[NVKETemporalAuthority](ctx, c, "setNvkeTemporalAuthorityEnabled", "SetNvkeTemporalAuthorityEnabledInput", nvkeTemporalFields, struct {
		NVKETemporalReview
		Enabled bool `json:"enabled"`
	}{input, enabled})
}
