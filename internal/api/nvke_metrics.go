package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

const nvkeMetricsAuthorityFields = `id version siteId siteVersion endpoint allowHttp isEnabled timeoutSeconds hasCredentials verifiedAt verificationCode deletedAt`

type NVKEMetricsCatalog struct {
	Revision          string          `json:"revision"`
	CredentialMetrics json.RawMessage `json:"credentialMetrics"`
}

func (c *Client) NVKEMetricsCatalog(ctx context.Context) (*NVKEMetricsCatalog, error) {
	var response struct {
		Catalog *NVKEMetricsCatalog `json:"astroliftNvkeContract"`
	}
	err := c.GraphQL(ctx, `query{astroliftNvkeContract{revision credentialMetrics}}`, nil, &response)
	if err != nil || response.Catalog == nil {
		return nil, errors.New("NVKE credential metrics catalog unavailable")
	}
	var shape map[string]json.RawMessage
	if json.Unmarshal(response.Catalog.CredentialMetrics, &shape) != nil || shape == nil {
		return nil, errors.New("NVKE credential metrics catalog incomplete")
	}
	return response.Catalog, nil
}

type NVKEMetricsAuthority struct {
	ID               string  `json:"id"`
	Version          int     `json:"version"`
	SiteID           string  `json:"siteId"`
	SiteVersion      int     `json:"siteVersion"`
	Endpoint         string  `json:"endpoint"`
	AllowHTTP        bool    `json:"allowHttp"`
	IsEnabled        bool    `json:"isEnabled"`
	TimeoutSeconds   int     `json:"timeoutSeconds"`
	HasCredentials   bool    `json:"hasCredentials"`
	VerifiedAt       *string `json:"verifiedAt"`
	VerificationCode string  `json:"verificationCode"`
	DeletedAt        *string `json:"deletedAt"`
}

type NVKEMetricsReview struct {
	SiteID              string `json:"siteId"`
	ExpectedActorID     int    `json:"expectedActorId"`
	ExpectedSiteVersion int    `json:"expectedSiteVersion"`
	ExpectedVersion     int    `json:"expectedVersion"`
}

type NVKEMetricsConfigureInput struct {
	NVKEMetricsReview
	Endpoint       string                `json:"endpoint"`
	AllowHTTP      bool                  `json:"allowHttp"`
	TimeoutSeconds int                   `json:"timeoutSeconds"`
	Credentials    *NVKECredentialsInput `json:"credentials,omitempty"`
}

func (c *Client) NVKEMetricsAuthority(ctx context.Context, site string) (*NVKEMetricsAuthority, error) {
	var response map[string]*NVKEMetricsAuthority
	err := c.GraphQL(ctx, `query($site:GUID!){astroliftNvkeMetricsAuthority(siteId:$site){`+nvkeMetricsAuthorityFields+`}}`, map[string]interface{}{"site": site}, &response)
	row, present := response["astroliftNvkeMetricsAuthority"]
	if err != nil || !present || (row != nil && row.SiteID != site) {
		return nil, errors.New("NVKE metrics authority metadata unavailable or mismatched")
	}
	return row, nil
}

func (c *Client) ConfigureNVKEMetricsAuthority(ctx context.Context, input NVKEMetricsConfigureInput) (*NVKEMetricsAuthority, error) {
	return nvkeMutation[NVKEMetricsAuthority](ctx, c, "configureNvkeMetricsAuthority", "ConfigureNvkeMetricsAuthorityInput", nvkeMetricsAuthorityFields, input)
}
func (c *Client) VerifyNVKEMetricsAuthority(ctx context.Context, input NVKEMetricsReview) (*NVKEMetricsAuthority, error) {
	return nvkeMutation[NVKEMetricsAuthority](ctx, c, "verifyNvkeMetricsAuthority", "NvkeMetricsAuthorityReviewInput", nvkeMetricsAuthorityFields, input)
}
func (c *Client) RemoveNVKEMetricsAuthority(ctx context.Context, input NVKEMetricsReview) (*NVKEMetricsAuthority, error) {
	return nvkeMutation[NVKEMetricsAuthority](ctx, c, "removeNvkeMetricsAuthority", "NvkeMetricsAuthorityReviewInput", nvkeMetricsAuthorityFields, input)
}
func (c *Client) SetNVKEMetricsAuthorityEnabled(ctx context.Context, input NVKEMetricsReview, enabled bool) (*NVKEMetricsAuthority, error) {
	return nvkeMutation[NVKEMetricsAuthority](ctx, c, "setNvkeMetricsAuthorityEnabled", "SetNvkeMetricsAuthorityEnabledInput", nvkeMetricsAuthorityFields, struct {
		NVKEMetricsReview
		Enabled bool `json:"enabled"`
	}{input, enabled})
}

type NVKECredentialRequestInput struct {
	SiteID                   string `json:"siteId"`
	ExpectedActorID          int    `json:"expectedActorId"`
	ExpectedSiteVersion      int    `json:"expectedSiteVersion"`
	ExpectedAuthorityVersion int    `json:"expectedAuthorityVersion"`
	SourceOperationID        string `json:"sourceOperationId"`
	ExpectedSourceVersion    int    `json:"expectedSourceVersion"`
	RequestID                string `json:"requestId"`
	TimeoutSeconds           int    `json:"timeoutSeconds"`
}

func (c *Client) RequestNVKECredentialObservation(ctx context.Context, input NVKECredentialRequestInput) (*NVKEOperation, error) {
	return nvkeMutation[NVKEOperation](ctx, c, "requestNvkeCredentialObservation", "RequestNvkeCredentialObservationInput", nvkeOperationFields, input)
}

type NVKECredentialView struct {
	SiteID           string `json:"siteId"`
	OperationID      string `json:"operationId"`
	OperationVersion int    `json:"operationVersion"`
	ScrapedAt        string `json:"scrapedAt"`
}

type NVKECredentialObservation struct {
	NVKECredentialView
	SourceSiteVersion        int     `json:"sourceSiteVersion"`
	ClusterID                string  `json:"clusterId"`
	SourceOperationID        string  `json:"sourceOperationId"`
	SourceVersion            int     `json:"sourceVersion"`
	AuthorityID              string  `json:"authorityId"`
	AuthorityVersion         int     `json:"authorityVersion"`
	ClusterReported          *bool   `json:"clusterReported"`
	CollectionSucceeded      *bool   `json:"collectionSucceeded"`
	MonitoringEnabled        *bool   `json:"monitoringEnabled"`
	RenewalAutomationEnabled *bool   `json:"renewalAutomationEnabled"`
	WarningWindowSeconds     *string `json:"warningWindowSeconds"`
}

type NVKECredentialMetric struct {
	Key        string  `json:"key"`
	Metric     string  `json:"metric"`
	Node       *string `json:"node"`
	Credential *string `json:"credential"`
	State      *string `json:"state"`
	Value      string  `json:"value"`
}

type NVKECredentialCertificate struct {
	Key                       string  `json:"key"`
	Node                      string  `json:"node"`
	Credential                string  `json:"credential"`
	Required                  *bool   `json:"required"`
	ObservationAvailable      *bool   `json:"observationAvailable"`
	LatestInspectionAvailable *bool   `json:"latestInspectionAvailable"`
	ObservationAgeSeconds     *string `json:"observationAgeSeconds"`
	ReportedNotAfter          *string `json:"reportedNotAfter"`
	ReportedExpiredAtScrape   *bool   `json:"reportedExpiredAtScrape"`
}

type NVKECredentialPage[T any] struct {
	NVKECredentialView
	NVKEPage[T]
}

func nvkeCredentialQuery[T any](ctx context.Context, c *Client, field, fields, site, op string, actor, version int, after string, limit int, search string, paged bool) (*T, error) {
	declarations, arguments := "", ""
	variables := map[string]interface{}{"site": site, "op": op, "actor": actor, "version": version}
	if paged {
		declarations = ",$after:String,$limit:Int!,$search:String"
		arguments = ",after:$after,limit:$limit,search:$search"
		variables["after"], variables["limit"], variables["search"] = nvkeCursor(after), limit, search
	}
	query := fmt.Sprintf(`query($site:GUID!,$op:GUID!,$actor:Int!,$version:Int!%s){%s(siteId:$site,operationId:$op,expectedActorId:$actor,expectedVersion:$version%s){ok errors{code} data{%s}}}`, declarations, field, arguments, fields)
	var response map[string]nvkeResult[T]
	if err := c.GraphQL(ctx, query, variables, &response); err != nil {
		return nil, errors.New("NVKE credential observation reply unavailable")
	}
	result, present := response[field]
	if !present {
		return nil, errors.New("NVKE credential observation reply incomplete")
	}
	return result.value()
}

const nvkeCredentialViewFields = `siteId operationId operationVersion scrapedAt`

func (c *Client) NVKECredentialObservation(ctx context.Context, site, op string, actor, version int) (*NVKECredentialObservation, error) {
	return nvkeCredentialQuery[NVKECredentialObservation](ctx, c, "astroliftNvkeCredentialObservation", nvkeCredentialViewFields+` sourceSiteVersion clusterId sourceOperationId sourceVersion authorityId authorityVersion clusterReported collectionSucceeded monitoringEnabled renewalAutomationEnabled warningWindowSeconds`, site, op, actor, version, "", 0, "", false)
}

func (c *Client) NVKECredentialMetrics(ctx context.Context, site, op string, actor, version int, after string, limit int, search string) (*NVKECredentialPage[NVKECredentialMetric], error) {
	return nvkeCredentialQuery[NVKECredentialPage[NVKECredentialMetric]](ctx, c, "astroliftNvkeCredentialMetricsPage", nvkeCredentialViewFields+` totalCount nextCursor items{key metric node credential state value}`, site, op, actor, version, after, limit, search, true)
}

func (c *Client) NVKECredentialCertificates(ctx context.Context, site, op string, actor, version int, after string, limit int, search string) (*NVKECredentialPage[NVKECredentialCertificate], error) {
	return nvkeCredentialQuery[NVKECredentialPage[NVKECredentialCertificate]](ctx, c, "astroliftNvkeCredentialCertificatesPage", nvkeCredentialViewFields+` totalCount nextCursor items{key node credential required observationAvailable latestInspectionAvailable observationAgeSeconds reportedNotAfter reportedExpiredAtScrape}`, site, op, actor, version, after, limit, search, true)
}
