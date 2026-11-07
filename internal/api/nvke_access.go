package api

import (
	"context"
	"errors"
)

const nvkeAccessFields = `id version siteId controllerConnectionId sourceOperationId clusterId upstreamRevision createdAt deletedAt`

type NVKEAccessBinding struct {
	ID                     string  `json:"id"`
	Version                int     `json:"version"`
	SiteID                 string  `json:"siteId"`
	ControllerConnectionID string  `json:"controllerConnectionId"`
	SourceOperationID      string  `json:"sourceOperationId"`
	ClusterID              string  `json:"clusterId"`
	UpstreamRevision       string  `json:"upstreamRevision"`
	CreatedAt              string  `json:"createdAt"`
	DeletedAt              *string `json:"deletedAt"`
}

type NVKEAccessEnrollInput struct {
	SiteID                string `json:"siteId"`
	ExpectedSiteVersion   int    `json:"expectedSiteVersion"`
	ExpectedActorID       int    `json:"expectedActorId"`
	SourceOperationID     string `json:"sourceOperationId"`
	ExpectedSourceVersion int    `json:"expectedSourceVersion"`
}

type NVKEAccessRevokeInput struct {
	SiteID              string `json:"siteId"`
	ExpectedSiteVersion int    `json:"expectedSiteVersion"`
	ExpectedActorID     int    `json:"expectedActorId"`
	BindingID           string `json:"bindingId"`
	ExpectedVersion     int    `json:"expectedVersion"`
}

func (c *Client) NVKEAccessBindings(ctx context.Context, site, after string, limit int) (*NVKEPage[NVKEAccessBinding], error) {
	var response struct {
		Page *NVKEPage[NVKEAccessBinding] `json:"astroliftNvkeWorkloadAccessBindings"`
	}
	err := c.GraphQL(ctx, `query($site:GUID!,$after:String,$limit:Int!){astroliftNvkeWorkloadAccessBindings(siteId:$site,after:$after,limit:$limit){items {`+nvkeAccessFields+`} nextCursor totalCount}}`, map[string]interface{}{"site": site, "after": nvkeCursor(after), "limit": limit}, &response)
	if err != nil || response.Page == nil {
		return nil, errors.New("NVKE access binding page unavailable")
	}
	for _, item := range response.Page.Items {
		if item.SiteID != site || item.DeletedAt != nil {
			return nil, errors.New("NVKE access binding page did not match the requested site")
		}
	}
	return response.Page, nil
}

func (c *Client) EnrollNVKEAccess(ctx context.Context, input NVKEAccessEnrollInput) (*NVKEAccessBinding, error) {
	return nvkeMutation[NVKEAccessBinding](ctx, c, "enrollNvkeWorkloadAccess", "EnrollNvkeWorkloadAccessInput", nvkeAccessFields, input)
}

func (c *Client) RevokeNVKEAccess(ctx context.Context, input NVKEAccessRevokeInput) (*NVKEAccessBinding, error) {
	return nvkeMutation[NVKEAccessBinding](ctx, c, "revokeNvkeWorkloadAccess", "RevokeNvkeWorkloadAccessInput", nvkeAccessFields, input)
}
