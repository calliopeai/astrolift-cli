package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

const resourceTestID = "01930000-0000-7000-8000-000000000001"

func resourceReadFixture() map[string]interface{} {
	return map[string]interface{}{"id": resourceTestID, "name": "db", "ownerScope": "project", "projectId": "p-1", "organizationId": "org-1", "contextRevision": "review-1", "status": "active"}
}
func resourceReadCmd() (*cobra.Command, *bytes.Buffer) {
	cmd, out := groupsTestCmd()
	cmd.Flags().Bool("page", false, "")
	cmd.Flags().Int("limit", 50, "")
	for _, name := range []string{"after", "search", "environment", "cluster", "resource", "expected-context-revision"} {
		cmd.Flags().String(name, "", "")
	}
	cmd.Flags().StringSlice("kind", nil, "")
	cmd.Flags().StringSlice("status", nil, "")
	return cmd, out
}
func TestProjectResourceDefaultArrayWalkReachesMoreThan200(t *testing.T) {
	requests := 0
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		requests++
		if strings.Contains(req.Query, "statusError") || strings.Contains(req.Query, " config") || strings.Contains(req.Query, "attachments") {
			t.Errorf("unsafe projection: %s", req.Query)
		}
		start := 0
		if value, ok := req.Variables["after"].(string); ok {
			if _, err := fmt.Sscanf(value, "cursor-%d", &start); err != nil {
				t.Fatal(err)
			}
		}
		rows := []interface{}{}
		end := min(start+int(req.Variables["limit"].(float64)), 251)
		for i := start; i < end; i++ {
			row := resourceReadFixture()
			row["id"] = fmt.Sprintf("01930000-0000-7000-8000-%012d", i)
			rows = append(rows, row)
		}
		var next interface{}
		if end < 251 {
			next = fmt.Sprintf("cursor-%d", end)
		}
		return map[string]interface{}{"astroliftProjectManagedServicesPage": map[string]interface{}{"items": rows, "totalCount": 251, "nextCursor": next}}
	})
	defer srv.Close()
	cmd, out := resourceReadCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runProjectResourceList(cmd, context.Background(), api.NewClient(srv.URL, "token", false), projectRef{ID: "p-1"}); err != nil {
		t.Fatal(err)
	}
	var rows []projectResource
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 251 || requests != 6 {
		t.Fatalf("rows=%d requests=%d", len(rows), requests)
	}
}
func TestProjectResourcePageKeepsFiltersCountsAndContinuation(t *testing.T) {
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{"astroliftProjectManagedServicesPage": map[string]interface{}{"items": []interface{}{resourceReadFixture()}, "totalCount": 251, "nextCursor": "next"}}, &captured)
	defer srv.Close()
	cmd, out := resourceReadCmd()
	for name, value := range map[string]string{"json": "true", "page": "true", "after": "previous", "search": "cache", "kind": "redis", "status": "active", "environment": "production", "cluster": resourceTestID, "limit": "25"} {
		_ = cmd.Flags().Set(name, value)
	}
	if err := runProjectResourceList(cmd, context.Background(), api.NewClient(srv.URL, "token", false), projectRef{ID: "p-1"}); err != nil {
		t.Fatal(err)
	}
	var page projectResourcePage
	if err := json.Unmarshal(out.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.TotalCount != 251 || page.NextCursor == nil || *page.NextCursor != "next" || captured.Variables["after"] != "previous" || captured.Variables["clusterId"] != resourceTestID {
		t.Fatalf("page=%+v variables=%+v", page, captured.Variables)
	}
}
func TestProjectResourceNameDiscoveryRereadsExactGUIDAndNeverReplacesMissing(t *testing.T) {
	requests := []gqlRequest{}
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		requests = append(requests, req)
		if strings.Contains(req.Query, "astroliftProjectManagedServicesPage") {
			return map[string]interface{}{"astroliftProjectManagedServicesPage": map[string]interface{}{"items": []interface{}{resourceReadFixture()}, "totalCount": 1, "nextCursor": nil}}
		}
		return map[string]interface{}{"astroliftProjectManagedService": nil}
	})
	defer srv.Close()
	_, err := resolveProjectResource(context.Background(), api.NewClient(srv.URL, "token", false), "p-1", "db")
	if err == nil || len(requests) != 2 || requests[0].Variables["limit"] != float64(2) || requests[1].Variables["id"] != resourceTestID {
		t.Fatalf("requests=%+v err=%v", requests, err)
	}
}
func TestProjectResourceReviewedWritesCarryContextAndHideFailureBodies(t *testing.T) {
	for _, action := range []string{"update", "reprovision", "attach", "detach", "remove"} {
		t.Run(action, func(t *testing.T) {
			var writes []gqlRequest
			srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
				if strings.Contains(req.Query, "astroliftProjectManagedService(") {
					return map[string]interface{}{"astroliftProjectManagedService": resourceReadFixture()}
				}
				writes = append(writes, req)
				field := ""
				for _, candidate := range []string{"updateProjectManagedService", "reprovisionProjectManagedService", "attachProjectManagedService", "detachProjectManagedService", "deprovisionProjectManagedService"} {
					if strings.Contains(req.Query, candidate+"(") {
						field = candidate
					}
				}
				return map[string]interface{}{field: map[string]interface{}{"ok": false, "errors": []interface{}{map[string]interface{}{"code": "STALE_TARGET", "message": "PRIVATE_CONFIG_MARKER"}}, "data": nil}}
			})
			defer srv.Close()
			cmd, out := resourceReadCmd()
			_ = cmd.Flags().Set("resource", resourceTestID)
			_ = cmd.Flags().Set("expected-context-revision", "review-1")
			client := api.NewClient(srv.URL, "token", false)
			ctx := context.Background()
			project := projectRef{ID: "p-1"}
			var err error
			switch action {
			case "update":
				err = runProjectResourceUpdate(cmd, ctx, client, project, resourceTestID, projectResourceOptions{Name: "new"})
			case "reprovision":
				err = runProjectResourceReprovision(cmd, ctx, client, project, resourceTestID)
			case "attach":
				err = runProjectResourceAttach(cmd, ctx, client, project, resourceTestID, projectResourceOptions{AppEnvironments: []string{resourceTestID}})
			case "detach":
				err = runProjectResourceDetach(cmd, ctx, client, project, resourceTestID)
			case "remove":
				err = runProjectResourceRemove(cmd, ctx, client, project, resourceTestID, projectResourceOptions{})
			}
			if err == nil || strings.Contains(err.Error(), "PRIVATE_CONFIG_MARKER") || out.Len() != 0 || len(writes) != 1 {
				t.Fatalf("out=%s err=%v writes=%+v", out, err, writes)
			}
			input := writes[0].Variables["input"].(map[string]interface{})
			if input["expectedContextRevision"] != "review-1" {
				t.Fatalf("unreviewed input=%+v", input)
			}
			if action == "detach" && input["managedServiceId"] != resourceTestID {
				t.Fatal("detach lost owner")
			}
		})
	}
}
func TestProjectResourceCostRejectsMissingOrUnsafeEvidence(t *testing.T) {
	for _, change := range []string{"amount", "source", "timestamp", "currency", "negative"} {
		t.Run(change, func(t *testing.T) {
			price := map[string]interface{}{"managedServiceId": resourceTestID, "available": true, "monthlyTotal": 0, "currency": "USD", "pricingSourceUrl": "https://prices.example.test", "pricingFetchedAt": "2026-10-02T00:00:00Z"}
			switch change {
			case "amount":
				price["monthlyTotal"] = nil
			case "source":
				price["pricingSourceUrl"] = "javascript:PRIVATE_CONFIG_MARKER"
			case "timestamp":
				price["pricingFetchedAt"] = "invalid"
			case "currency":
				price["currency"] = "invalid"
			case "negative":
				price["monthlyTotal"] = -1
			}
			srv := gqlServer(t, map[string]interface{}{"astroliftProjectManagedService": resourceReadFixture(), "astroliftManagedServiceCostPreview": price}, nil)
			defer srv.Close()
			cmd, out := resourceReadCmd()
			_ = cmd.Flags().Set("json", "true")
			if err := runProjectResourceCost(cmd, context.Background(), api.NewClient(srv.URL, "token", false), projectRef{ID: "p-1"}, resourceTestID); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "PRIVATE_CONFIG_MARKER") {
				t.Fatal("unsafe price source leaked")
			}
			var parsed projectResourceCostPreview
			if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
				t.Fatal(err)
			}
			if parsed.Available || parsed.MonthlyTotal != nil {
				t.Fatalf("missing price converted to estimate: %+v", parsed)
			}
		})
	}
}

func TestProjectResourceDetachLegacyInvocationResolvesExactOwnerThenPinsRevision(t *testing.T) {
	requests := []gqlRequest{}
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		requests = append(requests, req)
		switch {
		case strings.Contains(req.Query, "astroliftProjectManagedServiceAttachmentOwner("):
			if req.Variables["attachmentId"] != resourceTestID {
				t.Fatal("lost attachment GUID")
			}
			return map[string]interface{}{"astroliftProjectManagedServiceAttachmentOwner": resourceReadFixture()}
		case strings.Contains(req.Query, "astroliftProjectManagedService("):
			if req.Variables["id"] != resourceTestID || req.Variables["expectedContextRevision"] != "review-1" {
				t.Fatal("unreviewed owner reread")
			}
			return map[string]interface{}{"astroliftProjectManagedService": resourceReadFixture()}
		default:
			input := req.Variables["input"].(map[string]interface{})
			if input["attachmentId"] != resourceTestID || input["managedServiceId"] != resourceTestID || input["expectedContextRevision"] != "review-1" {
				t.Fatal("unreviewed detach")
			}
			return map[string]interface{}{"detachProjectManagedService": map[string]interface{}{"ok": true, "data": map[string]interface{}{"id": resourceTestID}}}
		}
	})
	defer srv.Close()
	cmd, out := resourceReadCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runProjectResourceDetach(cmd, context.Background(), api.NewClient(srv.URL, "token", false), projectRef{ID: "p-1"}, resourceTestID); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 || !strings.Contains(out.String(), resourceTestID) {
		t.Fatalf("requests=%+v output=%s", requests, out)
	}
	for _, req := range requests {
		if strings.Contains(req.Query, "ManagedServicesPage") {
			t.Fatal("owner lookup scanned catalog")
		}
	}
}

func TestProjectResourceDetachLegacyRefusesUnavailableOrChangedOwnerBeforeWrite(t *testing.T) {
	for _, change := range []string{"missing", "foreign", "bad_guid", "empty_revision", "review_changed", "owner_reread_missing", "owner_reread_changed"} {
		t.Run(change, func(t *testing.T) {
			requests := 0
			srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
				requests++
				owner := resourceReadFixture()
				if strings.Contains(req.Query, "astroliftProjectManagedServiceAttachmentOwner(") {
					switch change {
					case "missing":
						return map[string]interface{}{"astroliftProjectManagedServiceAttachmentOwner": nil}
					case "foreign":
						owner["projectId"] = "other"
					case "bad_guid":
						owner["id"] = "invalid"
					case "empty_revision":
						owner["contextRevision"] = ""
					}
					return map[string]interface{}{"astroliftProjectManagedServiceAttachmentOwner": owner}
				}
				if !strings.Contains(req.Query, "astroliftProjectManagedService(") {
					t.Fatal("refused owner performed mutation")
				}
				if change == "owner_reread_changed" {
					owner["contextRevision"] = "review-2"
					return map[string]interface{}{"astroliftProjectManagedService": owner}
				}
				return map[string]interface{}{"astroliftProjectManagedService": nil}
			})
			defer srv.Close()
			cmd, out := resourceReadCmd()
			if change == "review_changed" {
				_ = cmd.Flags().Set("expected-context-revision", "old-review")
			}
			err := runProjectResourceDetach(cmd, context.Background(), api.NewClient(srv.URL, "token", false), projectRef{ID: "p-1"}, resourceTestID)
			if err == nil || out.Len() != 0 || requests > 2 {
				t.Fatalf("err=%v requests=%d out=%s", err, requests, out)
			}
		})
	}
}
