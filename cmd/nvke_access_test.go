package cmd

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func nvkeAccessTestCommand(t *testing.T, verb string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	leaf, _, err := newNVKEAccessCommand().Find([]string{verb})
	if err != nil {
		t.Fatal(err)
	}
	leaf.SetContext(context.Background())
	out := &bytes.Buffer{}
	leaf.SetOut(out)
	leaf.SetErr(out)
	values := map[string]string{"site": nvkeTestSite}
	if verb != "list" {
		values["site-version"] = "7"
		if verb == "enroll" {
			values["source-version"] = "9"
		} else {
			values["version"] = "3"
		}
	}
	for flag, value := range values {
		if err := leaf.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	return leaf, out
}

func nvkeAccessTestBinding() map[string]any {
	return map[string]any{"id": nvkeTestOperation, "version": 3, "siteId": nvkeTestSite, "controllerConnectionId": uuid.NewString(), "sourceOperationId": uuid.NewString(), "clusterId": uuid.NewString(), "deletedAt": nil}
}

func TestNVKEAccessWritesRetainReviewedVersionsAndActor(t *testing.T) {
	for _, verb := range []string{"enroll", "revoke"} {
		t.Run(verb, func(t *testing.T) {
			cmd, out := nvkeAccessTestCommand(t, verb)
			mutations := 0
			server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
				data := nvkeBaseData()
				field := "enrollNvkeWorkloadAccess"
				if verb == "revoke" {
					field = "revokeNvkeWorkloadAccess"
				}
				if strings.Contains(request.Query, field+"(") {
					mutations++
					input := request.Variables["input"].(map[string]interface{})
					if input["siteId"] != nvkeTestSite || input["expectedSiteVersion"] != float64(7) || input["expectedActorId"] != float64(42) {
						t.Error("reviewed authority changed")
					}
					binding := nvkeAccessTestBinding()
					if verb == "enroll" {
						if input["sourceOperationId"] != nvkeTestOperation || input["expectedSourceVersion"] != float64(9) {
							t.Error("source review changed")
						}
					} else {
						if input["bindingId"] != nvkeTestOperation || input["expectedVersion"] != float64(3) {
							t.Error("binding review changed")
						}
						binding["deletedAt"] = "2026-11-07T00:00:00Z"
						binding["version"] = 4
					}
					data[field] = map[string]any{"ok": true, "data": binding}
				}
				writePipelineTestResponse(t, w, data)
			})
			defer server.Close()
			run := runNVKEAccessEnroll
			if verb == "revoke" {
				run = runNVKEAccessRevoke
			}
			if err := run(cmd, context.Background(), nvkeTestClient(server), []string{nvkeTestOperation}); err != nil {
				t.Fatal(err)
			}
			if mutations != 1 || !strings.Contains(out.String(), nvkeTestOperation) || strings.Contains(out.String(), "PRIVATE") {
				t.Fatal("write replayed or output unsafe")
			}
		})
	}
}

func TestNVKEAccessInvalidReviewsFailBeforeNetwork(t *testing.T) {
	for _, verb := range []string{"enroll", "revoke"} {
		for _, change := range []string{"site", "identity", "site-version", "version"} {
			t.Run(verb+"/"+change, func(t *testing.T) {
				cmd, _ := nvkeAccessTestCommand(t, verb)
				id := nvkeTestOperation
				flag := change
				if change == "identity" {
					id = "not-a-guid"
				} else {
					if change == "version" && verb == "enroll" {
						flag = "source-version"
					}
					value := "0"
					if change == "site" {
						value = "not-a-site"
					}
					_ = cmd.Flags().Set(flag, value)
				}
				calls := 0
				server := nvkeTestServer(t, func(_ gqlRequest, _ http.ResponseWriter) { calls++ })
				defer server.Close()
				run := runNVKEAccessEnroll
				if verb == "revoke" {
					run = runNVKEAccessRevoke
				}
				if err := run(cmd, context.Background(), nvkeTestClient(server), []string{id}); err == nil || calls != 0 {
					t.Fatal("invalid review reached network")
				}
			})
		}
	}
}

func TestNVKEAccessUncertainReplyIsNotRetriedOrDisclosed(t *testing.T) {
	for _, verb := range []string{"enroll", "revoke"} {
		t.Run(verb, func(t *testing.T) {
			cmd, out := nvkeAccessTestCommand(t, verb)
			mutations := 0
			server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
				if strings.HasPrefix(request.Query, "mutation") {
					mutations++
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte("PRIVATE_UPSTREAM_ERROR"))
					return
				}
				writePipelineTestResponse(t, w, nvkeBaseData())
			})
			defer server.Close()
			run := runNVKEAccessEnroll
			if verb == "revoke" {
				run = runNVKEAccessRevoke
			}
			err := run(cmd, context.Background(), nvkeTestClient(server), []string{nvkeTestOperation})
			if err == nil || mutations != 1 || !strings.Contains(err.Error(), "access list") || strings.Contains(err.Error()+out.String(), "PRIVATE") {
				t.Fatalf("unsafe uncertain reply: %v", err)
			}
		})
	}
}

func TestNVKEAccessListUsesServerCursorAndRejectsForeignRows(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		cmd, out := nvkeAccessTestCommand(t, "list")
		_ = cmd.Flags().Set("after", "opaque-next-cursor")
		_ = cmd.Flags().Set("limit", "1")
		server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
			if request.Variables["site"] != nvkeTestSite || request.Variables["after"] != "opaque-next-cursor" || request.Variables["limit"] != float64(1) {
				t.Error("server page scope changed")
			}
			binding := nvkeAccessTestBinding()
			if foreign {
				binding["siteId"] = uuid.NewString()
			}
			writePipelineTestResponse(t, w, map[string]any{"astroliftNvkeWorkloadAccessBindings": map[string]any{"items": []any{binding}, "nextCursor": "next", "totalCount": 8}})
		})
		err := runNVKEAccessList(cmd, context.Background(), nvkeTestClient(server), nil)
		server.Close()
		if foreign && (err == nil || out.Len() != 0) {
			t.Fatal("foreign page was printed")
		}
		if !foreign && (err != nil || !strings.Contains(out.String(), `"totalCount": 8`)) {
			t.Fatalf("page unavailable: %v", err)
		}
	}
}

func TestNVKEAccessWritesDoNotPrintMismatchedBindingReplies(t *testing.T) {
	for _, verb := range []string{"enroll", "revoke"} {
		for _, change := range []string{"site", "state"} {
			t.Run(verb+"/"+change, func(t *testing.T) {
				cmd, out := nvkeAccessTestCommand(t, verb)
				server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
					data := nvkeBaseData()
					if strings.HasPrefix(request.Query, "mutation") {
						binding := nvkeAccessTestBinding()
						field := "enrollNvkeWorkloadAccess"
						if verb == "revoke" {
							field = "revokeNvkeWorkloadAccess"
							if change == "site" {
								binding["deletedAt"] = "2026-11-07T00:00:00Z"
							}
						} else if change == "state" {
							binding["deletedAt"] = "2026-11-07T00:00:00Z"
						}
						if change == "site" {
							binding["siteId"] = uuid.NewString()
						}
						data[field] = map[string]any{"ok": true, "data": binding}
					}
					writePipelineTestResponse(t, w, data)
				})
				defer server.Close()
				run := runNVKEAccessEnroll
				if verb == "revoke" {
					run = runNVKEAccessRevoke
				}
				if err := run(cmd, context.Background(), nvkeTestClient(server), []string{nvkeTestOperation}); err == nil || out.Len() != 0 {
					t.Fatal("mismatched grant printed as success")
				}
			})
		}
	}
}
