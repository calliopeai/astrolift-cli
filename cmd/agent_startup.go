package cmd

import (
	"context"
	"errors"
	"strings"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

// startupDiagnostic is an observation, not a terminal task or box status.
type startupDiagnostic struct {
	Phase      string `json:"phase"`
	Reason     string `json:"reason"`
	Message    string `json:"message"`
	PodName    string `json:"podName"`
	ObservedAt string `json:"observedAt"`
}

func (d *startupDiagnostic) summary() string {
	if d == nil {
		return ""
	}
	reason, message := strings.TrimSpace(d.Reason), strings.TrimSpace(d.Message)
	if reason == "" {
		return message
	}
	if message == "" {
		return reason
	}
	return reason + ": " + message
}

// Read-only projections can adapt to an older server without retrying a
// mutation or hiding permission/transport failures. The original query stays
// intact for callers that do not need startup observations.
func queryStartupDiagnostic(ctx context.Context, client *api.Client, query string, vars map[string]interface{}, target interface{}) error {
	selection := "    status\n"
	extended := strings.Replace(query, selection, selection+"    startupDiagnostic { phase reason message podName observedAt }\n", 1)
	err := client.GraphQL(ctx, extended, vars, target)
	if err != nil && errors.Is(err, api.ErrSchemaMismatch) && strings.Contains(strings.ToLower(err.Error()), "startupdiagnostic") {
		return client.GraphQL(ctx, query, vars, target)
	}
	return err
}
