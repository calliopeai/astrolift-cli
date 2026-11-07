package api

import (
	"context"
	"errors"
)

type NVKECertificateRenewal struct {
	WorkflowID           *string `json:"workflowId"`
	State                *string `json:"state"`
	Outcome              *string `json:"outcome"`
	Forced               *bool   `json:"forced"`
	ScheduledFor         *string `json:"scheduledFor"`
	StartedAt            *string `json:"startedAt"`
	CompletedAt          *string `json:"completedAt"`
	FailedNodeID         *string `json:"failedNodeId"`
	Phase                *string `json:"phase"`
	RetainedMutationLock *bool   `json:"retainedMutationLock"`
}

type NVKEEtcdKeyRotation struct {
	WorkflowID           *string `json:"workflowId"`
	State                *string `json:"state"`
	Phase                *string `json:"phase"`
	StartedAt            *string `json:"startedAt"`
	LastProgressAt       *string `json:"lastProgressAt"`
	CompletedAt          *string `json:"completedAt"`
	FailureReason        *string `json:"failureReason"`
	RetainedMutationLock *bool   `json:"retainedMutationLock"`
}

type NVKEMaintenanceObservation struct {
	SiteID                              string                  `json:"siteId"`
	OperationID                         string                  `json:"operationId"`
	OperationVersion                    int                     `json:"operationVersion"`
	ObservedAt                          *string                 `json:"observedAt"`
	ClusterID                           string                  `json:"clusterId"`
	NextCertificateRenewalDue           *string                 `json:"nextCertificateRenewalDue"`
	RenewalScheduleOverdueAtObservation *bool                   `json:"renewalScheduleOverdueAtObservation"`
	MutationLockReported                *bool                   `json:"mutationLockReported"`
	MutationLockOwner                   *string                 `json:"mutationLockOwner"`
	MutationLockStartedAt               *string                 `json:"mutationLockStartedAt"`
	Renewal                             *NVKECertificateRenewal `json:"renewal"`
	EtcdKeyRotation                     *NVKEEtcdKeyRotation    `json:"etcdKeyRotation"`
}

func (c *Client) NVKEMaintenance(ctx context.Context, site, op string, actor, version int) (*NVKEMaintenanceObservation, error) {
	var response struct {
		Observation *NVKEMaintenanceObservation `json:"astroliftNvkeMaintenanceObservation"`
	}
	query := `query($site:GUID!,$id:GUID!,$actor:Int!,$version:Int!){astroliftNvkeMaintenanceObservation(siteId:$site,operationId:$id,expectedActorId:$actor,expectedVersion:$version){siteId operationId operationVersion observedAt clusterId nextCertificateRenewalDue renewalScheduleOverdueAtObservation mutationLockReported mutationLockOwner mutationLockStartedAt renewal {workflowId state outcome forced scheduledFor startedAt completedAt failedNodeId phase retainedMutationLock} etcdKeyRotation {workflowId state phase startedAt lastProgressAt completedAt failureReason retainedMutationLock}}}`
	err := c.GraphQL(ctx, query, map[string]interface{}{"site": site, "id": op, "actor": actor, "version": version}, &response)
	if err != nil || response.Observation == nil || response.Observation.SiteID != site || response.Observation.OperationID != op || response.Observation.OperationVersion != version {
		return nil, errors.New("NVKE maintenance observation unavailable")
	}
	return response.Observation, nil
}
