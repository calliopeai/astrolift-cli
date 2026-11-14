package api

import (
	"context"
	"errors"
)

type NVKENativeWorkflowInput struct {
	SiteID                      string `json:"siteId"`
	ExpectedSiteVersion         int    `json:"expectedSiteVersion"`
	ExpectedActorID             int    `json:"expectedActorId"`
	ExpectedAuthorityVersion    int    `json:"expectedAuthorityVersion"`
	RequestID                   string `json:"requestId"`
	Workflow                    string `json:"workflow"`
	ParametersText              string `json:"parametersText"`
	SourceOperationID           string `json:"sourceOperationId"`
	ExpectedSourceVersion       int    `json:"expectedSourceVersion"`
	AcknowledgeLostNodeTeardown bool   `json:"acknowledgeLostNodeTeardown"`
	TimeoutSeconds              int    `json:"timeoutSeconds"`
}

func (c *Client) RequestNVKENativeWorkflow(ctx context.Context, input NVKENativeWorkflowInput) (*NVKEOperation, error) {
	return nvkeMutation[NVKEOperation](ctx, c, "requestNvkeNativeWorkflow", "RequestNvkeNativeWorkflowInput", nvkeOperationFields, input)
}

func (c *Client) NVKENativeRequestReceipt(ctx context.Context, site, requestID string) (*NVKEOperation, error) {
	var response map[string]*NVKEOperation
	err := c.GraphQL(ctx, `query($site:GUID!,$request:String!){astroliftNvkeRequest(siteId:$site,requestId:$request){`+nvkeOperationFields+`}}`, map[string]interface{}{"site": site, "request": requestID}, &response)
	row, present := response["astroliftNvkeRequest"]
	if err != nil || !present {
		return nil, errors.New("native NVKE receipt unavailable; preserve the original recovery identity")
	}
	return row, nil
}
