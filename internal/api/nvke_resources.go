package api

import (
	"context"
	"encoding/json"
	"errors"
)

type NVKECollection struct {
	Key                   string       `json:"key"`
	Surface               string       `json:"surface"`
	ResourceKind          string       `json:"resourceKind"`
	Namespaced            bool         `json:"namespaced"`
	Permission            string       `json:"permission"`
	DefaultParametersText string       `json:"defaultParametersText"`
	Actions               []NVKEAction `json:"actions"`
}
type NVKERequestTextInput struct {
	SiteID              string `json:"siteId"`
	ExpectedSiteVersion int    `json:"expectedSiteVersion"`
	ExpectedActorID     int    `json:"expectedActorId"`
	RequestID           string `json:"requestId"`
	OperationKey        string `json:"operationKey"`
	ParametersText      string `json:"parametersText"`
	TimeoutSeconds      int    `json:"timeoutSeconds"`
}
type NVKEResourceRequestInput struct {
	NVKERequestTextInput
	SourceOperationID     string  `json:"sourceOperationId"`
	ExpectedSourceVersion int     `json:"expectedSourceVersion"`
	SourceResourceKey     *string `json:"sourceResourceKey"`
}

func (c *Client) RequestNVKEText(ctx context.Context, input NVKERequestTextInput, inventory bool) (*NVKEOperation, error) {
	field := "requestNvkeOperationText"
	if inventory {
		field = "requestNvkeInventory"
	}
	return nvkeMutation[NVKEOperation](ctx, c, field, "RequestNvkeOperationTextInput", nvkeOperationFields, input)
}
func (c *Client) RequestNVKEResource(ctx context.Context, input NVKEResourceRequestInput) (*NVKEOperation, error) {
	return nvkeMutation[NVKEOperation](ctx, c, "requestNvkeResourceOperation", "RequestNvkeResourceOperationInput", nvkeOperationFields, input)
}
func (c *Client) NVKERequest(ctx context.Context, site, requestID string) (*NVKEOperation, error) {
	var response struct {
		Operation *NVKEOperation `json:"astroliftNvkeRequest"`
	}
	err := c.GraphQL(ctx, `query($site:GUID!,$request:String!){astroliftNvkeRequest(siteId:$site,requestId:$request){`+nvkeOperationFields+`}}`, map[string]interface{}{"site": site, "request": requestID}, &response)
	if err != nil {
		return nil, errors.New("NVKE receipt unavailable; preserve the original recovery identity")
	}
	return response.Operation, nil
}

type NVKEInventoryItem struct {
	Key               string `json:"key"`
	Identity          string `json:"identity"`
	Name              string `json:"name"`
	UID               string `json:"uid"`
	Namespace         string `json:"namespace"`
	ResourceVersion   string `json:"resourceVersion"`
	Generation        string `json:"generation"`
	KubernetesVersion string `json:"kubernetesVersion"`
	DesiredCount      string `json:"desiredCount"`
	Conditions        []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"conditions"`
	ConditionsOmitted int `json:"conditionsOmitted"`
}

const nvkeInventoryFields = `key identity name uid namespace resourceVersion generation kubernetesVersion desiredCount conditions {name status} conditionsOmitted`
const nvkeCollectionFields = `key surface resourceKind namespaced permission defaultParametersText actions {key surface verb resourceKind mutates permission path}`

type NVKEInventoryPage struct {
	NVKEPage[NVKEInventoryItem]
	OperationID     string         `json:"operationId"`
	ObservedAt      *string        `json:"observedAt"`
	Collection      NVKECollection `json:"collection"`
	ResourceVersion *string        `json:"resourceVersion"`
	NativePageCount int            `json:"nativePageCount"`
}

func (c *Client) NVKEInventoryPage(ctx context.Context, op string, actor int, after string, limit int, search string) (*NVKEInventoryPage, error) {
	var response struct {
		Page *NVKEInventoryPage `json:"astroliftNvkeInventoryPage"`
	}
	query := `query($id:GUID!,$actor:Int!,$after:String,$limit:Int!,$search:String){astroliftNvkeInventoryPage(operationId:$id,expectedActorId:$actor,after:$after,limit:$limit,search:$search){operationId observedAt resourceVersion nativePageCount collection {` + nvkeCollectionFields + `} items {` + nvkeInventoryFields + `} nextCursor totalCount}}`
	err := c.GraphQL(ctx, query, map[string]interface{}{"id": op, "actor": actor, "after": nvkeCursor(after), "limit": limit, "search": search}, &response)
	if err != nil || response.Page == nil || response.Page.OperationID != op {
		return nil, errors.New("NVKE inventory page unavailable")
	}
	return response.Page, nil
}

type NVKEInventoryResource struct {
	SiteID           string            `json:"siteId"`
	OperationID      string            `json:"operationId"`
	OperationVersion int               `json:"operationVersion"`
	ObservedAt       *string           `json:"observedAt"`
	Collection       NVKECollection    `json:"collection"`
	Item             NVKEInventoryItem `json:"item"`
}

func (c *Client) NVKEInventoryResource(ctx context.Context, op string, actor int, key string) (*NVKEInventoryResource, error) {
	var response struct {
		Resource *NVKEInventoryResource `json:"astroliftNvkeInventoryResource"`
	}
	query := `query($id:GUID!,$actor:Int!,$key:String!){astroliftNvkeInventoryResource(operationId:$id,expectedActorId:$actor,resourceKey:$key){siteId operationId operationVersion observedAt collection {` + nvkeCollectionFields + `} item {` + nvkeInventoryFields + `}}}`
	err := c.GraphQL(ctx, query, map[string]interface{}{"id": op, "actor": actor, "key": key}, &response)
	if err != nil || response.Resource == nil || response.Resource.OperationID != op || response.Resource.Item.Key != key {
		return nil, errors.New("NVKE resource observation unavailable")
	}
	return response.Resource, nil
}
func (c *Client) NVKEInventoryDocument(ctx context.Context, op string, actor int, key string) (string, error) {
	var response struct {
		Result nvkeResult[string] `json:"astroliftNvkeInventoryDocumentText"`
	}
	err := c.GraphQL(ctx, `query($id:GUID!,$actor:Int!,$key:String!){astroliftNvkeInventoryDocumentText(operationId:$id,expectedActorId:$actor,resourceKey:$key){ok errors {code} data}}`, map[string]interface{}{"id": op, "actor": actor, "key": key}, &response)
	if err != nil {
		return "", errors.New("NVKE resource document unavailable")
	}
	value, err := response.Result.value()
	if err != nil {
		return "", err
	}
	return *value, nil
}

type NVKEResourceEditor struct {
	OperationKey         string             `json:"operationKey"`
	ParametersText       *string            `json:"parametersText,omitempty"`
	PreparationActionKey *string            `json:"preparationActionKey"`
	Concurrency          string             `json:"concurrency"`
	Source               NVKEResourceSource `json:"source"`
}

func (c *Client) NVKEResourceEditor(ctx context.Context, site string, actor int, key string, source NVKEResourceSource) (*NVKEResourceEditor, error) {
	var response struct {
		Result nvkeResult[NVKEResourceEditor] `json:"astroliftNvkeResourceEditor"`
	}
	query := `query($site:GUID!,$actor:Int!,$key:String!,$source:GUID!,$version:Int!,$resource:String){astroliftNvkeResourceEditor(siteId:$site,expectedActorId:$actor,operationKey:$key,sourceOperationId:$source,expectedSourceVersion:$version,sourceResourceKey:$resource){ok errors {code} data {operationKey parametersText preparationActionKey concurrency source {operationId version resourceKey}}}}`
	err := c.GraphQL(ctx, query, map[string]interface{}{"site": site, "actor": actor, "key": key, "source": source.OperationID, "version": source.Version, "resource": source.ResourceKey}, &response)
	if err != nil {
		return nil, errors.New("NVKE resource editor unavailable")
	}
	return response.Result.value()
}
func (c *Client) NVKEOperationResultText(ctx context.Context, id string) (string, error) {
	var response struct {
		Result nvkeResult[string] `json:"astroliftNvkeOperationResultText"`
	}
	err := c.GraphQL(ctx, `query($id:GUID!){astroliftNvkeOperationResultText(operationId:$id){ok errors {code} data}}`, map[string]interface{}{"id": id}, &response)
	if err != nil {
		return "", errors.New("NVKE operation result unavailable")
	}
	value, err := response.Result.value()
	if err != nil {
		return "", err
	}
	if !json.Valid([]byte(*value)) {
		return "", errors.New("NVKE result is not a JSON document")
	}
	return *value, nil
}

type NVKEDiagnosticRow struct {
	Key               string   `json:"key"`
	NativeID          *string  `json:"nativeId"`
	Name              *string  `json:"name"`
	CreatedAt         *string  `json:"createdAt"`
	IsCurrent         *bool    `json:"isCurrent"`
	Reachable         *bool    `json:"reachable"`
	HasValidSVID      *bool    `json:"hasValidSvid"`
	ServerAddress     *string  `json:"serverAddress"`
	ServerPort        *int     `json:"serverPort"`
	SVIDExpiresAt     *string  `json:"svidExpiresAt"`
	ChangedFields     []string `json:"changedFields"`
	ChangedFieldCount *string  `json:"changedFieldCount"`
	FieldsOmitted     *string  `json:"fieldsOmitted"`
}
type NVKEDiagnosticPage struct {
	NVKEPage[NVKEDiagnosticRow]
	SiteID           string  `json:"siteId"`
	OperationID      string  `json:"operationId"`
	OperationVersion int     `json:"operationVersion"`
	ObservedAt       *string `json:"observedAt"`
	Summary          struct {
		ClusterID         string  `json:"clusterId"`
		Kind              string  `json:"kind"`
		ConfigurationID   *string `json:"configurationId"`
		KubernetesVersion *string `json:"kubernetesVersion"`
		RequestedAddress  *string `json:"requestedAddress"`
		RequestedPort     *int    `json:"requestedPort"`
		Counts            []struct {
			Name  string  `json:"name"`
			Value *string `json:"value"`
		} `json:"counts"`
		Flags []struct {
			Name  string `json:"name"`
			Value *bool  `json:"value"`
		} `json:"flags"`
		Conditions []struct {
			Name  string  `json:"name"`
			Value *string `json:"value"`
		} `json:"conditions"`
	} `json:"summary"`
}

func (c *Client) NVKEDiagnosticPage(ctx context.Context, site, op string, actor, version int, after string, limit int, search string) (*NVKEDiagnosticPage, error) {
	var response struct {
		Page *NVKEDiagnosticPage `json:"astroliftNvkeDiagnosticPage"`
	}
	query := `query($site:GUID!,$id:GUID!,$actor:Int!,$version:Int!,$after:String,$limit:Int!,$search:String){astroliftNvkeDiagnosticPage(siteId:$site,operationId:$id,expectedActorId:$actor,expectedVersion:$version,after:$after,limit:$limit,search:$search){siteId operationId operationVersion observedAt totalCount nextCursor summary {clusterId kind configurationId kubernetesVersion requestedAddress requestedPort counts {name value} flags {name value} conditions {name value}} items {key nativeId name createdAt isCurrent reachable hasValidSvid serverAddress serverPort svidExpiresAt changedFields changedFieldCount fieldsOmitted}}}`
	err := c.GraphQL(ctx, query, map[string]interface{}{"site": site, "id": op, "actor": actor, "version": version, "after": nvkeCursor(after), "limit": limit, "search": search}, &response)
	if err != nil || response.Page == nil || response.Page.SiteID != site || response.Page.OperationID != op || response.Page.OperationVersion != version {
		return nil, errors.New("NVKE diagnostic page unavailable")
	}
	return response.Page, nil
}

func nvkeCursor(value string) any {
	if value == "" {
		return nil
	}
	return value
}
