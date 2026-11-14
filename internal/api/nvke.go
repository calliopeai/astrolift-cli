package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
)

const nvkeConnectionFields = `id version surface endpoint isEnabled allowHttp timeoutSeconds hasCredentials verifiedAt verificationCode`
const nvkeSiteFields = `id version name slug organizationNamespace isActive upstreamRevision createdAt updatedAt connections { ` + nvkeConnectionFields + ` }`
const nvkeOperationFields = `providerSource { connectionId connectionVersion siteVersion upstreamRevision } isInventory resourceSource { operationId version resourceKey } id version siteId requestId operationKey status stage generation errorCode errorMessage cancellationRequestedAt startedAt finishedAt deadline createdAt updatedAt`

// NVKE credentials are write-only. Results may hold native scripts or kubeconfigs
// and are fetched separately from the ordinary operation metadata.
type NVKEConnection struct {
	ID               string  `json:"id"`
	Version          int     `json:"version"`
	Surface          string  `json:"surface"`
	Endpoint         string  `json:"endpoint"`
	IsEnabled        bool    `json:"isEnabled"`
	AllowHTTP        bool    `json:"allowHttp"`
	TimeoutSeconds   int     `json:"timeoutSeconds"`
	HasCredentials   bool    `json:"hasCredentials"`
	VerifiedAt       *string `json:"verifiedAt"`
	VerificationCode string  `json:"verificationCode"`
}
type NVKESite struct {
	ID                    string           `json:"id"`
	Version               int              `json:"version"`
	Name                  string           `json:"name"`
	Slug                  string           `json:"slug"`
	OrganizationNamespace string           `json:"organizationNamespace"`
	IsActive              bool             `json:"isActive"`
	UpstreamRevision      string           `json:"upstreamRevision"`
	Connections           []NVKEConnection `json:"connections"`
	CreatedAt             string           `json:"createdAt"`
	UpdatedAt             string           `json:"updatedAt"`
}
type NVKEResourceSource struct {
	OperationID string  `json:"operationId"`
	Version     int     `json:"version"`
	ResourceKey *string `json:"resourceKey"`
}
type NVKEOperation struct {
	ProviderSource          *NVKEProviderSource `json:"providerSource"`
	IsInventory             bool                `json:"isInventory"`
	ResourceSource          *NVKEResourceSource `json:"resourceSource"`
	ID                      string              `json:"id"`
	Version                 int                 `json:"version"`
	SiteID                  string              `json:"siteId"`
	RequestID               string              `json:"requestId"`
	OperationKey            string              `json:"operationKey"`
	Status                  string              `json:"status"`
	Stage                   string              `json:"stage"`
	Generation              int                 `json:"generation"`
	ErrorCode               string              `json:"errorCode"`
	ErrorMessage            string              `json:"errorMessage"`
	CancellationRequestedAt *string             `json:"cancellationRequestedAt"`
	StartedAt               *string             `json:"startedAt"`
	FinishedAt              *string             `json:"finishedAt"`
	Deadline                string              `json:"deadline"`
	CreatedAt               string              `json:"createdAt"`
	UpdatedAt               string              `json:"updatedAt"`
}
type NVKEPage[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"nextCursor"`
	TotalCount int     `json:"totalCount"`
}
type NVKEAction struct {
	Key          string `json:"key"`
	Surface      string `json:"surface"`
	Verb         string `json:"verb"`
	ResourceKind string `json:"resourceKind"`
	Mutates      bool   `json:"mutates"`
	Permission   string `json:"permission"`
	Path         string `json:"path"`
}
type NVKEContract struct {
	NativeWorkflows   []NVKENativeWorkflow `json:"nativeWorkflows"`
	Revision          string               `json:"revision"`
	Actions           []NVKEAction         `json:"actions"`
	Collections       []NVKECollection     `json:"collections"`
	ControllerOpenAPI json.RawMessage      `json:"controllerOpenapi"`
	Resources         []struct {
		Kind       string          `json:"kind"`
		Namespaced bool            `json:"namespaced"`
		Schema     json.RawMessage `json:"schema"`
	} `json:"resources"`
}
type NVKECredentialsInput struct {
	Token                string `json:"token,omitempty"`
	CAPEM                string `json:"caPem,omitempty"`
	ClientCertificatePEM string `json:"clientCertificatePem,omitempty"`
	ClientKeyPEM         string `json:"clientKeyPem,omitempty"`
}
type NVKEConnectionInput struct {
	SiteID                    string                `json:"siteId"`
	ExpectedSiteVersion       int                   `json:"expectedSiteVersion"`
	Surface                   string                `json:"surface"`
	ExpectedConnectionVersion int                   `json:"expectedConnectionVersion"`
	Endpoint                  string                `json:"endpoint"`
	Credentials               *NVKECredentialsInput `json:"credentials,omitempty"`
	AllowHTTP                 bool                  `json:"allowHttp"`
	TimeoutSeconds            int                   `json:"timeoutSeconds"`
}
type NVKERequestInput struct {
	SiteID              string          `json:"siteId"`
	ExpectedSiteVersion int             `json:"expectedSiteVersion"`
	RequestID           string          `json:"requestId"`
	OperationKey        string          `json:"operationKey"`
	Parameters          json.RawMessage `json:"parameters"`
	TimeoutSeconds      int             `json:"timeoutSeconds"`
}
type nvkeResult[T any] struct {
	OK     bool `json:"ok"`
	Data   *T   `json:"data"`
	Errors []struct {
		Code string `json:"code"`
	} `json:"errors"`
}

var nvkeErrorCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

func (r nvkeResult[T]) value() (*T, error) {
	if !r.OK || r.Data == nil || len(r.Errors) > 0 {
		code := "REQUEST_REFUSED"
		if len(r.Errors) > 0 && nvkeErrorCode.MatchString(r.Errors[0].Code) {
			code = r.Errors[0].Code
		}
		return nil, fmt.Errorf("NVKE request refused (%s)", code)
	}
	return r.Data, nil
}
func nvkeMutation[T any](ctx context.Context, c *Client, field, inputType, fields string, input any) (*T, error) {
	query := fmt.Sprintf(`mutation($input: %s!) { %s(input:$input) { ok errors { code } data { %s } } }`, inputType, field, fields)
	var response map[string]nvkeResult[T]
	if err := c.GraphQL(ctx, query, map[string]interface{}{"input": input}, &response); err != nil {
		return nil, errors.New("NVKE mutation reply unavailable; preserve the original request identity to reconcile")
	}
	result, ok := response[field]
	if !ok {
		return nil, errors.New("NVKE mutation result unavailable")
	}
	return result.value()
}
func (c *Client) NVKEContract(ctx context.Context) (*NVKEContract, error) {
	var response struct {
		Contract *NVKEContract `json:"astroliftNvkeContract"`
	}
	err := c.GraphQL(ctx, `query { astroliftNvkeContract { revision actions { key surface verb resourceKind mutates permission path } nativeWorkflows { name inputType source permission inputSchema } collections { key surface resourceKind namespaced permission defaultParametersText actions { key surface verb resourceKind mutates permission path } } controllerOpenapi resources { kind namespaced schema } } }`, nil, &response)
	if err != nil || response.Contract == nil {
		return nil, errors.New("NVKE contract unavailable; check server support and organization permissions")
	}
	return response.Contract, nil
}
func (c *Client) NVKESites(ctx context.Context, after string, limit int) (*NVKEPage[NVKESite], error) {
	var response struct {
		Page *NVKEPage[NVKESite] `json:"astroliftNvkeSites"`
	}
	query := `query($after:String,$limit:Int!) { astroliftNvkeSites(after:$after,limit:$limit) { items { ` + nvkeSiteFields + ` } nextCursor totalCount } }`
	err := c.GraphQL(ctx, query, map[string]interface{}{"after": after, "limit": limit}, &response)
	if err != nil || response.Page == nil {
		return nil, errors.New("NVKE site page unavailable")
	}
	return response.Page, nil
}
func (c *Client) NVKESite(ctx context.Context, id string) (*NVKESite, error) {
	var response struct {
		Site *NVKESite `json:"astroliftNvkeSite"`
	}
	err := c.GraphQL(ctx, `query($id:GUID!) { astroliftNvkeSite(siteId:$id) { `+nvkeSiteFields+` } }`, map[string]interface{}{"id": id}, &response)
	if err != nil || response.Site == nil || response.Site.ID != id {
		return nil, errors.New("NVKE site unavailable")
	}
	return response.Site, nil
}
func (c *Client) CreateNVKESite(ctx context.Context, name, slug, namespace string) (*NVKESite, error) {
	return nvkeMutation[NVKESite](ctx, c, "createNvkeSite", "CreateNvkeSiteInput", nvkeSiteFields, map[string]interface{}{"name": name, "slug": slug, "organizationNamespace": namespace})
}
func (c *Client) ConfigureNVKEConnection(ctx context.Context, input NVKEConnectionInput) (*NVKESite, error) {
	return nvkeMutation[NVKESite](ctx, c, "configureNvkeConnection", "ConfigureNvkeConnectionInput", nvkeSiteFields, input)
}
func (c *Client) SetNVKESiteEnabled(ctx context.Context, id string, version int, enabled bool) (*NVKESite, error) {
	return nvkeMutation[NVKESite](ctx, c, "setNvkeSiteEnabled", "SetNvkeSiteEnabledInput", nvkeSiteFields, map[string]interface{}{"siteId": id, "expectedSiteVersion": version, "enabled": enabled})
}
func (c *Client) RemoveNVKESite(ctx context.Context, id string, version int) (*NVKESite, error) {
	return nvkeMutation[NVKESite](ctx, c, "removeNvkeSite", "RemoveNvkeSiteInput", nvkeSiteFields, map[string]interface{}{"siteId": id, "expectedSiteVersion": version})
}
func (c *Client) RequestNVKEOperation(ctx context.Context, input NVKERequestInput) (*NVKEOperation, error) {
	return nvkeMutation[NVKEOperation](ctx, c, "requestNvkeOperation", "RequestNvkeOperationInput", nvkeOperationFields, input)
}
func (c *Client) NVKEOperations(ctx context.Context, site, after string, limit int) (*NVKEPage[NVKEOperation], error) {
	var response struct {
		Page *NVKEPage[NVKEOperation] `json:"astroliftNvkeOperations"`
	}
	query := `query($site:GUID!,$after:String,$limit:Int!) { astroliftNvkeOperations(siteId:$site,after:$after,limit:$limit) { items { ` + nvkeOperationFields + ` } nextCursor totalCount } }`
	err := c.GraphQL(ctx, query, map[string]interface{}{"site": site, "after": after, "limit": limit}, &response)
	if err != nil || response.Page == nil {
		return nil, errors.New("NVKE operation page unavailable")
	}
	return response.Page, nil
}
func (c *Client) NVKEOperation(ctx context.Context, id string) (*NVKEOperation, error) {
	var response struct {
		Operation *NVKEOperation `json:"astroliftNvkeOperation"`
	}
	err := c.GraphQL(ctx, `query($id:GUID!) { astroliftNvkeOperation(operationId:$id) { `+nvkeOperationFields+` } }`, map[string]interface{}{"id": id}, &response)
	if err != nil {
		// Keep failure classes, never the underlying error text: transport URLs,
		// GraphQL messages and malformed response bytes can contain private data.
		var refusal *GraphQLResponseError
		var httpFailure *HTTPError
		var transport net.Error
		switch {
		case errors.As(err, &refusal):
			return nil, fmt.Errorf("NVKE operation unavailable (GraphQL refusal; HTTP %d)", refusal.Status)
		case errors.As(err, &httpFailure):
			return nil, fmt.Errorf("NVKE operation unavailable (HTTP %d)", httpFailure.Status)
		case errors.Is(err, context.Canceled):
			return nil, errors.New("NVKE operation unavailable (observation cancelled)")
		case errors.Is(err, context.DeadlineExceeded):
			return nil, errors.New("NVKE operation unavailable (observation deadline)")
		case errors.As(err, &transport):
			return nil, errors.New("NVKE operation unavailable (transport failure)")
		default:
			return nil, errors.New("NVKE operation unavailable (invalid or incomplete response)")
		}
	}
	if response.Operation == nil {
		return nil, errors.New("NVKE operation unavailable (receipt not visible)")
	}
	if response.Operation.ID != id {
		return nil, errors.New("NVKE operation unavailable (receipt identity mismatch)")
	}
	return response.Operation, nil
}
func (c *Client) NVKEOperationResult(ctx context.Context, id string) (json.RawMessage, error) {
	var response struct {
		Result nvkeResult[json.RawMessage] `json:"astroliftNvkeOperationResult"`
	}
	err := c.GraphQL(ctx, `query($id:GUID!) { astroliftNvkeOperationResult(operationId:$id) { ok errors { code } data } }`, map[string]interface{}{"id": id}, &response)
	if err != nil {
		return nil, errors.New("NVKE operation result unavailable")
	}
	result, err := response.Result.value()
	if err != nil {
		return nil, err
	}
	return *result, nil
}
func (c *Client) CancelNVKEOperation(ctx context.Context, id string) (*NVKEOperation, error) {
	return nvkeMutation[NVKEOperation](ctx, c, "cancelNvkeOperation", "CancelNvkeOperationInput", nvkeOperationFields, map[string]interface{}{"operationId": id})
}
func (c *Client) ResumeNVKEOperation(ctx context.Context, id string, version, timeout int) (*NVKEOperation, error) {
	return nvkeMutation[NVKEOperation](ctx, c, "resumeNvkeOperation", "ResumeNvkeOperationInput", nvkeOperationFields, map[string]interface{}{"operationId": id, "expectedVersion": version, "timeoutSeconds": timeout})
}
