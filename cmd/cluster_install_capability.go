package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

func requireClusterInstallCapability(ctx context.Context, client *api.Client, capability string) error {
	var response struct {
		Info *struct {
			Capabilities []string `json:"capabilities"`
		} `json:"astroliftServerInfo"`
	}
	query := `query ClusterInstallCapabilities { astroliftServerInfo { capabilities } }`
	if client.PublicGraphQL(ctx, query, nil, &response) != nil {
		return errors.New("public installation discovery at /app/gql/config/public/ is unavailable; ask the operator to expose that route; no installation was submitted")
	}
	if response.Info != nil {
		for _, value := range response.Info.Capabilities {
			if value == capability {
				return nil
			}
		}
	}
	return fmt.Errorf("selected server does not advertise %s; use a compatible server and preserve any original request file", capability)
}

// serverAdvertises reports whether public discovery lists capability. Any
// discovery failure reads as false, so callers keep their older behavior.
func serverAdvertises(ctx context.Context, client *api.Client, capability string) bool {
	var response struct {
		Info *struct {
			Capabilities []string `json:"capabilities"`
		} `json:"astroliftServerInfo"`
	}
	query := `query ServerCapabilities { astroliftServerInfo { capabilities } }`
	if client.PublicGraphQL(ctx, query, nil, &response) != nil || response.Info == nil {
		return false
	}
	for _, value := range response.Info.Capabilities {
		if value == capability {
			return true
		}
	}
	return false
}
