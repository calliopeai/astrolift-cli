package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/calliopeai/astrolift-cli/internal/api"
	"strings"
	"testing"
)

func TestPreviewExactIDBypassesRecentCatalogAndPricing(t *testing.T) {
	resetPreviewsFlags()
	defer resetPreviewsFlags()
	p := prPreview(1, "oldest")
	p.RuntimeStatus = "not_requested"
	var captured []gqlRequest
	srv := previewsServer(t, func(req gqlRequest) map[string]interface{} {
		if req.Variables["id"] != p.ID {
			t.Fatalf("exact GUID lost: %+v", req.Variables)
		}
		if req.Variables["includeRuntimeCost"] != false {
			t.Fatal("basic detail requested pricing")
		}
		return previewPage([]previewEnvironment{p}, "")
	}, &captured)
	defer srv.Close()
	cmd, out := appTestCmd()
	if err := runAppPreviewsShow(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", previewSelector{id: p.ID}); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 1 || !strings.Contains(captured[0].Query, "astroliftPreviewEnvironment(") || strings.Contains(captured[0].Query, "astroliftPreviewEnvironmentsPage") {
		t.Fatalf("must make one singular read: %+v", captured)
	}
	if !strings.Contains(out.String(), "not_requested") || strings.Contains(out.String(), "$0.00") {
		t.Fatalf("invented runtime: %s", out.String())
	}
}

func TestPreviewExactShowCostIsExplicit(t *testing.T) {
	p := prPreview(12, "feature")
	var captured []gqlRequest
	srv := previewsServer(t, func(gqlRequest) map[string]interface{} { return previewPage([]previewEnvironment{p}, "") }, &captured)
	defer srv.Close()
	cmd, _ := appTestCmd()
	cmd.Flags().Bool("cost", true, "")
	if err := runAppPreviewsShow(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", previewSelector{id: p.ID}); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 1 || captured[0].Variables["includeRuntimeCost"] != true {
		t.Fatalf("cost opt-in lost: %+v", captured)
	}
}

func TestPreviewExactReadRefusesWrongAppOrChangedSelection(t *testing.T) {
	for _, change := range []string{"app", "guid", "version", "missing"} {
		t.Run(change, func(t *testing.T) {
			p := prPreview(12, "feature")
			requested := p.ID
			switch change {
			case "app":
				p.RegisteredAppSlug = "other"
			case "guid":
				p.ID = prPreview(99, "other").ID
			case "version":
				p.Version = -1
			}
			srv := previewsServer(t, func(gqlRequest) map[string]interface{} {
				if change == "missing" {
					return previewPage(nil, "")
				}
				return previewPage([]previewEnvironment{p}, "")
			}, nil)
			defer srv.Close()
			cmd, _ := appTestCmd()
			sel := previewSelector{id: requested}
			if err := runAppPreviewsShow(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", sel); err == nil {
				t.Fatal("changed identity accepted")
			}
		})
	}
}

func TestPreviewPRDiscoveryRereadsAndRefusesChangedSelector(t *testing.T) {
	p := prPreview(12, "feature")
	calls := 0
	srv := previewsServer(t, func(gqlRequest) map[string]interface{} {
		calls++
		if calls == 2 {
			p.PRNumber = 13
		}
		return previewPage([]previewEnvironment{p}, "")
	}, nil)
	defer srv.Close()
	cmd, _ := appTestCmd()
	if err := runAppPreviewsShow(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", previewSelector{pr: 12, prSet: true}); err == nil {
		t.Fatal("changed PR accepted")
	}
	if calls != 2 {
		t.Fatalf("missing exact reread: %d", calls)
	}
}

func TestPreviewRetainedHostnameNeverAuthorizesOpenOrLogs(t *testing.T) {
	for _, change := range []string{"retired", "unavailable", "missing", "torn_down", "timestamp", "environment_guid", "preview_version", "app_slug", "namespace"} {
		t.Run(change, func(t *testing.T) {
			resetPreviewsFlags()
			defer resetPreviewsFlags()
			p := prPreview(12, "feature")
			switch change {
			case "retired", "unavailable":
				p.EnvironmentStatus = change
			case "missing":
				p.Environment = nil
			case "torn_down":
				p.Status = change
			case "timestamp":
				p.TornDownAt = strptr("2026-10-01T00:00:00Z")
			case "environment_guid":
				p.Environment.EnvironmentID = "invalid"
			case "preview_version":
				p.Environment.PreviewVersion++
			case "app_slug":
				p.Environment.AppSlug = "other"
			case "namespace":
				p.Environment.Namespace = "production"
			}
			var captured []gqlRequest
			srv := previewsServer(t, func(gqlRequest) map[string]interface{} { return previewPage([]previewEnvironment{p}, "") }, &captured)
			defer srv.Close()
			cmd, out := appTestCmd()
			previewsOpenURLOnly = true
			if err := runAppPreviewsOpen(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", previewSelector{id: p.ID}); err == nil {
				t.Fatal("invalid binding opened")
			}
			if out.Len() != 0 {
				t.Fatal("retained hostname exposed as action")
			}
			if err := runAppPreviewsLogs(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", previewSelector{id: p.ID}); err == nil {
				t.Fatal("invalid binding read logs")
			}
			if len(captured) != 2 {
				t.Fatalf("unexpected effect request %+v", captured)
			}
		})
	}
}

func TestPreviewLogsCarriesExactProofAcrossAllPages(t *testing.T) {
	resetPreviewsFlags()
	defer resetPreviewsFlags()
	appLogsSince = "1h"
	appLogsTail = 200
	appLogsFollow = false
	appLogsEnv = "production"
	defer func() { appLogsEnv = "" }()
	p := prPreview(12, "feature")
	p.Environment.EnvironmentName = "nonconventional-name"
	var captured []gqlRequest
	pages := 0
	srv := previewsServer(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "astroliftAppLogs") {
			pages++
			next := interface{}(nil)
			if pages == 1 {
				next = "page-two"
			}
			return map[string]interface{}{"astroliftAppLogs": map[string]interface{}{"items": []map[string]interface{}{{"timestamp": "2026-10-01T00:00:00Z", "message": fmt.Sprint(pages)}}, "nextCursor": next, "historicalAvailable": true}}
		}
		return previewPage([]previewEnvironment{p}, "")
	}, &captured)
	defer srv.Close()
	cmd, _ := appTestCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runAppPreviewsLogs(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", previewSelector{id: p.ID}); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 3 {
		t.Fatalf("expected exact read and two log pages: %d", len(captured))
	}
	for _, req := range captured[1:] {
		expected := map[string]interface{}{"previewId": p.ID, "expectedEnvironmentId": p.Environment.EnvironmentID, "ifMatchPreviewVersion": float64(p.Version), "ifMatchEnvironmentVersion": float64(p.Environment.EnvironmentVersion), "environmentName": "nonconventional-name"}
		for key, value := range expected {
			if req.Variables[key] != value {
				t.Fatalf("%s lost: %+v", key, req.Variables)
			}
		}
		if strings.Contains(req.Query, "astroliftEnvironments(") {
			t.Fatal("name catalog fallback")
		}
	}
	if appLogsEnv != "production" {
		t.Fatal("preview logs mutated another command's selected environment")
	}
}

func TestPreviewSelectorsRejectMixedOrMalformedExactIdentity(t *testing.T) {
	p := prPreview(12, "feature")
	for _, sel := range []previewSelector{{id: "bad"}, {id: p.ID, pr: 12, prSet: true}, {id: p.ID, branch: "feature"}} {
		if sel.validate() == nil {
			t.Fatalf("accepted selector %+v", sel)
		}
	}
	raw, _ := json.Marshal(p)
	if !strings.Contains(string(raw), `"environmentId"`) {
		t.Fatal("canonical identity absent from JSON")
	}
}
