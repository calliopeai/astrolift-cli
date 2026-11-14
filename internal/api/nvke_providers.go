package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type NVKEProviderSource struct {
	ConnectionID      string `json:"connectionId"`
	ConnectionVersion int    `json:"connectionVersion"`
	SiteVersion       int    `json:"siteVersion"`
	UpstreamRevision  string `json:"upstreamRevision"`
}
type NVKEProviderRequestInput struct {
	SiteID                    string `json:"siteId"`
	ExpectedActorID           int    `json:"expectedActorId"`
	ExpectedSiteVersion       int    `json:"expectedSiteVersion"`
	ConnectionID              string `json:"connectionId"`
	ExpectedConnectionVersion int    `json:"expectedConnectionVersion"`
	RequestID                 string `json:"requestId"`
	TimeoutSeconds            int    `json:"timeoutSeconds"`
}

func (c *Client) RequestNVKEProviderObservation(ctx context.Context, input NVKEProviderRequestInput) (*NVKEOperation, error) {
	return nvkeMutation[NVKEOperation](ctx, c, "requestNvkeProviderObservation", "RequestNvkeProviderObservationInput", nvkeOperationFields, input)
}

type NVKEProviderView struct {
	SiteID           string `json:"siteId"`
	OperationID      string `json:"operationId"`
	OperationVersion int    `json:"operationVersion"`
	ObservedAt       string `json:"observedAt"`
}
type NVKEProviderCredentials struct {
	Health                  string  `json:"health"`
	Reason                  string  `json:"reason"`
	IdentityNamespace       *string `json:"identityNamespace"`
	IdentityName            *string `json:"identityName"`
	IdentityGeneration      *string `json:"identityGeneration"`
	ReadyStatus             *string `json:"readyStatus"`
	ObservedGeneration      *string `json:"observedGeneration"`
	LastCheckedTime         *string `json:"lastCheckedTime"`
	NextCheckTime           *string `json:"nextCheckTime"`
	AdditionalIdentityCount *int    `json:"additionalIdentityCount"`
}
type NVKEInfrastructureProvider struct {
	Provider            string                   `json:"provider"`
	State               string                   `json:"state"`
	Reason              string                   `json:"reason"`
	Complete            *bool                    `json:"complete"`
	CRDsEstablished     *bool                    `json:"crdsEstablished"`
	ControllerAvailable *bool                    `json:"controllerAvailable"`
	ManagerNamespace    *string                  `json:"managerNamespace"`
	ManagerName         *string                  `json:"managerName"`
	ReadyReplicas       *string                  `json:"readyReplicas"`
	DesiredReplicas     *string                  `json:"desiredReplicas"`
	SecretNamespace     *string                  `json:"secretNamespace"`
	SecretName          *string                  `json:"secretName"`
	SecretPresent       *bool                    `json:"secretPresent"`
	EndpointConfigured  *bool                    `json:"endpointConfigured"`
	UnconfiguredIsValid *bool                    `json:"unconfiguredIsValid"`
	Credentials         *NVKEProviderCredentials `json:"credentials"`
}
type NVKEProviderObservation struct {
	NVKEProviderView
	SourceSiteVersion int                          `json:"sourceSiteVersion"`
	ConnectionID      string                       `json:"connectionId"`
	ConnectionVersion int                          `json:"connectionVersion"`
	UpstreamRevision  string                       `json:"upstreamRevision"`
	Complete          *bool                        `json:"complete"`
	Providers         []NVKEInfrastructureProvider `json:"providers"`
}
type NVKEProviderIdentity struct {
	Key       string `json:"key"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}
type NVKEProviderIdentityPage struct {
	NVKEProviderView
	NVKEPage[NVKEProviderIdentity]
	Provider string `json:"provider"`
}

const nvkeProviderViewFields = `siteId operationId operationVersion observedAt`

func (c *Client) NVKEProviderObservation(ctx context.Context, site, op string, actor, version int) (*NVKEProviderObservation, error) {
	fields := nvkeProviderViewFields + ` sourceSiteVersion connectionId connectionVersion upstreamRevision complete providers{provider state reason complete crdsEstablished controllerAvailable managerNamespace managerName readyReplicas desiredReplicas secretNamespace secretName secretPresent endpointConfigured unconfiguredIsValid credentials{health reason identityNamespace identityName identityGeneration readyStatus observedGeneration lastCheckedTime nextCheckTime additionalIdentityCount}}`
	query := `query($site:GUID!,$op:GUID!,$actor:Int!,$version:Int!){astroliftNvkeProviderObservation(siteId:$site,operationId:$op,expectedActorId:$actor,expectedVersion:$version){ok errors{code} data{` + fields + `}}}`
	var response map[string]nvkeResult[NVKEProviderObservation]
	if c.GraphQL(ctx, query, map[string]interface{}{"site": site, "op": op, "actor": actor, "version": version}, &response) != nil {
		return nil, errors.New("NVKE provider observation unavailable")
	}
	result, present := response["astroliftNvkeProviderObservation"]
	if !present {
		return nil, errors.New("NVKE provider observation incomplete")
	}
	return result.value()
}
func (c *Client) NVKEProviderIdentities(ctx context.Context, site, op string, actor, version int, provider, after string, limit int, search string) (*NVKEProviderIdentityPage, error) {
	query := fmt.Sprintf(`query($site:GUID!,$op:GUID!,$actor:Int!,$version:Int!,$provider:String!,$after:String,$limit:Int!,$search:String!){astroliftNvkeProviderIdentitiesPage(siteId:$site,operationId:$op,expectedActorId:$actor,expectedVersion:$version,provider:$provider,after:$after,limit:$limit,search:$search){ok errors{code} data{%s provider totalCount nextCursor items{key namespace name}}}}`, nvkeProviderViewFields)
	var response map[string]nvkeResult[NVKEProviderIdentityPage]
	if c.GraphQL(ctx, query, map[string]interface{}{"site": site, "op": op, "actor": actor, "version": version, "provider": provider, "after": nvkeCursor(after), "limit": limit, "search": search}, &response) != nil {
		return nil, errors.New("NVKE provider identities unavailable")
	}
	result, present := response["astroliftNvkeProviderIdentitiesPage"]
	if !present {
		return nil, errors.New("NVKE provider identities incomplete")
	}
	return result.value()
}

type NVKEProviderCatalog struct {
	Revision                string          `json:"revision"`
	InfrastructureProviders json.RawMessage `json:"infrastructureProviders"`
}

func (c *Client) NVKEProviderCatalog(ctx context.Context) (*NVKEProviderCatalog, error) {
	var response struct {
		Catalog *NVKEProviderCatalog `json:"astroliftNvkeContract"`
	}
	if c.GraphQL(ctx, `query{astroliftNvkeContract{revision infrastructureProviders}}`, nil, &response) != nil || response.Catalog == nil {
		return nil, errors.New("NVKE provider catalog unavailable")
	}
	return response.Catalog, nil
}
