package api

import (
	"context"
	"errors"
)

type NVKENativeHistoryInput struct {
	SiteID                   string  `json:"siteId"`
	ExpectedSiteVersion      int     `json:"expectedSiteVersion"`
	ExpectedActorID          int     `json:"expectedActorId"`
	ExpectedAuthorityVersion int     `json:"expectedAuthorityVersion"`
	SourceOperationID        string  `json:"sourceOperationId"`
	ExpectedSourceVersion    int     `json:"expectedSourceVersion"`
	Workflow                 string  `json:"workflow"`
	After                    *string `json:"after,omitempty"`
	Limit                    int     `json:"limit"`
}

type NVKENativeHistoryEvent struct {
	EventID                      string  `json:"eventId"`
	EventType                    string  `json:"eventType"`
	OccurredAt                   *string `json:"occurredAt"`
	ActivityType                 *string `json:"activityType"`
	ScheduledEventID             *string `json:"scheduledEventId"`
	WorkflowTaskCompletedEventID *string `json:"workflowTaskCompletedEventId"`
}

type NVKENativeHistoryPage struct {
	SiteID               string                   `json:"siteId"`
	SiteVersion          int                      `json:"siteVersion"`
	AuthorityVersion     int                      `json:"authorityVersion"`
	SourceOperationID    string                   `json:"sourceOperationId"`
	SourceVersion        int                      `json:"sourceVersion"`
	SourceObservedAt     *string                  `json:"sourceObservedAt"`
	ClusterID            string                   `json:"clusterId"`
	Workflow             string                   `json:"workflow"`
	WorkflowID           string                   `json:"workflowId"`
	RunID                string                   `json:"runId"`
	Status               string                   `json:"status"`
	RecordedState        *string                  `json:"recordedState"`
	RecordedPhase        *string                  `json:"recordedPhase"`
	RetainedMutationLock *bool                    `json:"retainedMutationLock"`
	ObservedAt           string                   `json:"observedAt"`
	Items                []NVKENativeHistoryEvent `json:"items"`
	NextCursor           *string                  `json:"nextCursor"`
}

func (c *Client) NVKENativeHistory(ctx context.Context, input NVKENativeHistoryInput) (*NVKENativeHistoryPage, error) {
	var response map[string]nvkeResult[NVKENativeHistoryPage]
	err := c.GraphQL(ctx, `query($input:ReadNvkeNativeHistoryInput!){astroliftNvkeNativeHistory(input:$input){ok errors{code} data{siteId siteVersion authorityVersion sourceOperationId sourceVersion sourceObservedAt clusterId workflow workflowId runId status recordedState recordedPhase retainedMutationLock observedAt items{eventId eventType occurredAt activityType scheduledEventId workflowTaskCompletedEventId} nextCursor}}}`, map[string]interface{}{"input": input}, &response)
	result, present := response["astroliftNvkeNativeHistory"]
	if err != nil || !present {
		return nil, errors.New("native NVKE history unavailable; retain the exact review and cursor")
	}
	return result.value()
}

type NVKENativeResetReview struct {
	WorkflowID                        string `json:"workflowId"`
	BaseRunID                         string `json:"baseRunId"`
	WorkflowTaskEventID               string `json:"workflowTaskEventId"`
	ActivityEventID                   string `json:"activityEventId"`
	ActivityType                      string `json:"activityType"`
	Phase                             string `json:"phase"`
	Mode                              string `json:"mode"`
	Rationale                         string `json:"rationale"`
	QuiescenceEvidence                string `json:"quiescenceEvidence"`
	AcknowledgeBoundaryReviewed       bool   `json:"acknowledgeBoundaryReviewed"`
	AcknowledgeConflictingWorkStopped bool   `json:"acknowledgeConflictingWorkStopped"`
	RetirementWorkflowID              string `json:"retirementWorkflowId"`
}

type NVKENativeResetInput struct {
	NVKENativeResetReview
	SiteID                   string `json:"siteId"`
	ExpectedSiteVersion      int    `json:"expectedSiteVersion"`
	ExpectedActorID          int    `json:"expectedActorId"`
	ExpectedAuthorityVersion int    `json:"expectedAuthorityVersion"`
	SourceOperationID        string `json:"sourceOperationId"`
	ExpectedSourceVersion    int    `json:"expectedSourceVersion"`
	RequestID                string `json:"requestId"`
	Workflow                 string `json:"workflow"`
	TimeoutSeconds           int    `json:"timeoutSeconds"`
}

func (c *Client) RequestNVKENativeReset(ctx context.Context, input NVKENativeResetInput) (*NVKEOperation, error) {
	return nvkeMutation[NVKEOperation](ctx, c, "requestNvkeNativeReset", "RequestNvkeNativeResetInput", nvkeOperationFields, input)
}
