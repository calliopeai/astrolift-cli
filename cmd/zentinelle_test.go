package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
)

func resetZentinelleFlags() {
	zentinelleURL, zentinelleCode = "", ""
	zentinelleCodeStdin, zentinelleForce, zentinelleYes = false, false, false
	zentinelleOverlapSec = 0
}

var zentinelleOrg = map[string]interface{}{
	"astroliftOrganizations": []map[string]string{{"id": "org-guid", "slug": "acme", "name": "Acme"}},
}

func TestZentinelleStatusSaysNotConnectedOrListsLiveGateways(t *testing.T) {
	resetZentinelleFlags()
	connection := map[string]interface{}(nil)
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "astroliftOrganizations") {
			return zentinelleOrg
		}
		if req.Organization != "org-guid" {
			t.Errorf("status read without the working org: %q", req.Organization)
		}
		return map[string]interface{}{"astroliftZentinelleConnection": connection}
	})
	defer srv.Close()
	client := api.NewClient(srv.URL, "token", false)

	cmd, out := appTestCmd()
	if err := runZentinelleStatus(cmd, context.Background(), client, &config.Config{}, "https://astro.example"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Zentinelle is not connected on https://astro.example") {
		t.Fatalf("output: %s", out.String())
	}

	connection = map[string]interface{}{
		"baseUrl": "https://zentinelle.example", "status": "connected", "gatewayFeatureEnabled": true,
		"clusters": []map[string]interface{}{
			{"clusterSlug": "prod", "status": "active", "gatewayEnabled": true, "gatewayDeployed": true},
			{"clusterSlug": "old", "status": "revoked", "unregistered": true},
		},
	}
	cmd, out = appTestCmd()
	if err := runZentinelleStatus(cmd, context.Background(), client, &config.Config{}, "https://astro.example"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "https://zentinelle.example (connected)") || !strings.Contains(out.String(), "prod") || strings.Contains(out.String(), "old") {
		t.Fatalf("output: %s", out.String())
	}
}

func TestZentinelleConnectReadsTheCodeFromStdinAndSurfacesRefusals(t *testing.T) {
	resetZentinelleFlags()
	t.Cleanup(resetZentinelleFlags)
	refuse := false
	var sent gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "astroliftOrganizations") {
			return zentinelleOrg
		}
		sent = req
		if refuse {
			return map[string]interface{}{"connectZentinelle": map[string]interface{}{
				"ok": false, "errors": []map[string]string{{"code": "PERMISSION_DENIED", "message": "api token scope does not allow this permission: zentinelle.connect"}},
			}}
		}
		return map[string]interface{}{"connectZentinelle": map[string]interface{}{
			"ok": true, "data": map[string]interface{}{"baseUrl": "https://zentinelle.example", "status": "connected"},
		}}
	})
	defer srv.Close()
	client := api.NewClient(srv.URL, "token", false)
	zentinelleURL, zentinelleCodeStdin = "https://zentinelle.example", true

	cmd, out := appTestCmd()
	cmd.SetIn(strings.NewReader("enroll-123\n"))
	if err := runZentinelleConnect(cmd, context.Background(), client, &config.Config{}, ""); err != nil {
		t.Fatal(err)
	}
	input := sent.Variables["input"].(map[string]interface{})
	if input["enrollmentCode"] != "enroll-123" || input["url"] != "https://zentinelle.example" {
		t.Fatalf("connect input: %#v", input)
	}
	if !strings.Contains(out.String(), "Connected to Zentinelle") {
		t.Fatalf("output: %s", out.String())
	}

	refuse = true
	cmd, _ = appTestCmd()
	cmd.SetIn(strings.NewReader("enroll-123"))
	err := runZentinelleConnect(cmd, context.Background(), client, &config.Config{}, "")
	if err == nil || !strings.Contains(err.Error(), "zentinelle.connect") {
		t.Fatalf("expected the server's refusal, got %v", err)
	}
}

func TestZentinelleDestructiveVerbsNeedYes(t *testing.T) {
	resetZentinelleFlags()
	t.Cleanup(resetZentinelleFlags)
	cmd, _ := appTestCmd()
	client := api.NewClient("http://127.0.0.1:1", "token", false)
	if err := runZentinelleDisconnect(cmd, context.Background(), client, &config.Config{}, ""); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("disconnect without --yes: %v", err)
	}
	if err := zentinelleUnregisterCmd.RunE(cmd, []string{"prod"}); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("unregister without --yes must refuse before any request: %v", err)
	}
}

func TestZentinelleGatewayVerbsSendTheResolvedCluster(t *testing.T) {
	resetZentinelleFlags()
	t.Cleanup(resetZentinelleFlags)
	var sent gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		sent = req
		return map[string]interface{}{"setZentinelleGatewayEnabled": map[string]interface{}{
			"ok": true, "data": map[string]interface{}{"clusterSlug": "prod", "status": "active", "gatewayEnabled": false},
		}}
	})
	defer srv.Close()
	cmd, out := appTestCmd()
	err := runGatewayMutation(cmd, context.Background(), api.NewClient(srv.URL, "token", false), "setZentinelleGatewayEnabled",
		"SetZentinelleGatewayEnabledInput", map[string]interface{}{"clusterId": "cluster-guid", "enabled": false}, "Disabled the gateway on")
	if err != nil {
		t.Fatal(err)
	}
	input := sent.Variables["input"].(map[string]interface{})
	if input["clusterId"] != "cluster-guid" || input["enabled"] != false || !strings.Contains(sent.Query, "SetZentinelleGatewayEnabledInput!") {
		t.Fatalf("gateway request: %#v %s", input, sent.Query)
	}
	if !strings.Contains(out.String(), "Disabled the gateway on prod (active)") {
		t.Fatalf("output: %s", out.String())
	}
}
