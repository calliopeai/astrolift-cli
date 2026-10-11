package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
)

func capabilityServer(t *testing.T, advertised bool, orgs int, perms []string) func() map[string]interface{} {
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		switch {
		case strings.Contains(req.Query, "astroliftServerInfo"):
			caps := []string{"server_info.handshake"}
			if advertised {
				caps = append(caps, "boxes.custom_image_permission")
			}
			return map[string]interface{}{"astroliftServerInfo": map[string]interface{}{"capabilities": caps}}
		case strings.Contains(req.Query, "astroliftOrganizations"):
			list := []map[string]string{{"id": "org-a", "slug": "a", "name": "A"}}
			if orgs > 1 {
				list = append(list, map[string]string{"id": "org-b", "slug": "b", "name": "B"})
			}
			return map[string]interface{}{"astroliftOrganizations": list}
		case strings.Contains(req.Query, "astroliftMyPermissions"):
			if req.Organization != "org-a" {
				t.Errorf("permissions read for org %q", req.Organization)
			}
			return map[string]interface{}{"astroliftMyPermissions": perms}
		}
		return nil
	})
	t.Cleanup(srv.Close)
	return func() map[string]interface{} {
		cmd, _ := appTestCmd()
		return boxCapabilities(cmd, context.Background(), api.NewClient(srv.URL, "token", false), &config.Config{})
	}
}

func customImage(caps map[string]interface{}) (bool, bool) {
	box, ok := caps["box"].(map[string]interface{})
	if !ok {
		return false, false
	}
	allowed, ok := box["customImage"].(bool)
	return allowed, ok
}

func TestBoxCustomImageCapabilityFollowsTheServersPermission(t *testing.T) {
	if allowed, ok := customImage(capabilityServer(t, true, 1, []string{"agent.dispatch", "agent_box.custom_image"})()); !ok || !allowed {
		t.Fatalf("expected allowed, got %v %v", allowed, ok)
	}
	if allowed, ok := customImage(capabilityServer(t, true, 1, []string{"agent.dispatch"})()); !ok || allowed {
		t.Fatalf("expected denied, got %v %v", allowed, ok)
	}
}

func TestBoxCustomImageCapabilityIsUnreportedWhenUnknown(t *testing.T) {
	if caps := capabilityServer(t, false, 1, []string{"agent_box.custom_image"})(); caps != nil {
		t.Fatalf("an older server must leave the capability unreported, got %#v", caps)
	}
	if caps := capabilityServer(t, true, 2, []string{"agent_box.custom_image"})(); caps != nil {
		t.Fatalf("an ambiguous org must leave the capability unreported, got %#v", caps)
	}
}
