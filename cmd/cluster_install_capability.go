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
		return errors.New("installation capability discovery is unavailable; no installation was submitted")
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
