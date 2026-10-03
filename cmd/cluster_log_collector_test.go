package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const collectorTestSource = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func collectorCommand(t *testing.T) (*cobra.Command, *bytes.Buffer, string) {
	t.Helper()
	c, out, file := agentInstallCommand(t)
	c.Flags().Int("retention-days", 30, "")
	return c, out, file
}
func collectorHTTPData() map[string]interface{} {
	data := agentInstallData()
	data["astroliftClusterLogCollectorReview"] = map[string]interface{}{"clusterId": reviewedPipelineTestID, "version": 7, "source": collectorTestSource, "retentionDays": 30, "supported": true, "refusalCode": "", "message": "", "policy": map[string]interface{}{}, "readerPolicy": map[string]interface{}{}}
	return data
}
func collectorResult(input map[string]interface{}, status string) map[string]interface{} {
	return map[string]interface{}{"id": agentInstallTestID, "clusterId": input["clusterId"], "requestId": input["requestId"], "expectedVersion": input["expectedVersion"], "expectedSource": input["expectedSource"], "retentionDays": input["retentionDays"], "status": status, "stage": "queued", "retryable": true, "cleanupPending": false, "coverage": "UNVERIFIED", "workflowId": "InstallClusterLogCollectorWorkflow-" + agentInstallTestID, "deadline": "2026-10-03T03:00:00Z", "postLossVerifiedAt": nil, "activatedAt": nil, "activatedClusterVersion": nil, "errorCode": "", "errorMessage": "", "readerPolicy": map[string]interface{}{}, "createdAt": "2026-10-03T01:00:00Z", "updatedAt": "2026-10-03T01:00:00Z"}
}
func TestCollectorInstallDurableBeforeDispatchAndLostReplyReusesOriginalTuple(t *testing.T) {
	c, out, file := collectorCommand(t)
	inputs := []map[string]interface{}{}
	reviews := 0
	srv := agentInstallServer(t, func(q gqlRequest, w http.ResponseWriter) {
		data := collectorHTTPData()
		if strings.Contains(q.Query, "query ClusterLogCollectorReview") {
			reviews++
			if reviews > 1 {
				t.Error("recovery refreshed review")
			}
		}
		if strings.Contains(q.Query, "mutation InstallClusterLogCollector") {
			input := q.Variables["input"].(map[string]interface{})
			inputs = append(inputs, input)
			saved, err := readCollectorInstallRequest(file)
			if err != nil || saved.RequestID != input["requestId"] || saved.TargetID != input["clusterId"] || saved.ExpectedSource != input["expectedSource"] {
				t.Errorf("not durable: %v", err)
			}
			if len(inputs) == 1 {
				w.WriteHeader(503)
				_, _ = w.Write([]byte("PRIVATE_RESPONSE_BODY"))
				return
			}
			data["astroliftInstallClusterLogCollector"] = map[string]interface{}{"ok": true, "data": collectorResult(input, "QUEUED")}
		}
		writePipelineTestResponse(t, w, data)
	})
	defer srv.Close()
	client := api.NewClient(srv.URL, "PRIVATE_BEARER", false)
	client.SetOrg(reviewedPipelineTestOrg)
	err := runClusterInstallLogCollector(c, context.Background(), client, "production", 30)
	if err == nil || !strings.Contains(err.Error(), "unknown") || strings.Contains(err.Error(), "PRIVATE_RESPONSE_BODY") {
		t.Fatalf("lost response unsafe: %v", err)
	}
	before, _ := os.ReadFile(file)
	if err := runClusterInstallLogCollector(c, context.Background(), client, "production", 30); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(file)
	if reviews != 1 || len(inputs) != 2 || !reflect.DeepEqual(inputs[0], inputs[1]) || !bytes.Equal(before, after) {
		t.Fatal("original tuple not retained")
	}
	if strings.Contains(string(after)+out.String(), "PRIVATE_BEARER") || !strings.Contains(out.String(), "not yet activated") {
		t.Fatal("unsafe or false completion output")
	}
}
func TestCollectorRecoveryRefusesChangedTupleAndMissingSourceBeforeMutation(t *testing.T) {
	for _, change := range []string{"actor", "organization", "server", "slug", "retention", "source", "missingtarget"} {
		t.Run(change, func(t *testing.T) {
			c, _, file := collectorCommand(t)
			mutations, reviews := 0, 0
			srv := agentInstallServer(t, func(q gqlRequest, w http.ResponseWriter) {
				data := collectorHTTPData()
				if change == "actor" {
					data["astroliftMyProfile"] = map[string]interface{}{"userId": 43}
				}
				if strings.Contains(q.Query, "mutation") {
					mutations++
				}
				if strings.Contains(q.Query, "Review") {
					reviews++
				}
				writePipelineTestResponse(t, w, data)
			})
			defer srv.Close()
			r := collectorRequestFixture()
			r.Server = srv.URL
			if change == "server" {
				r.Server = "https://different.invalid"
			}
			if change == "organization" {
				r.OrganizationID = uuid.NewString()
			}
			if err := createCollectorInstallRequest(file, r); err != nil {
				t.Fatal(err)
			}
			if change == "source" || change == "missingtarget" {
				raw, _ := os.ReadFile(file)
				var body map[string]interface{}
				_ = json.Unmarshal(raw, &body)
				if change == "source" {
					delete(body, "expectedSource")
				} else {
					delete(body, "targetId")
				}
				raw, _ = json.Marshal(body)
				if err := os.WriteFile(file, raw, 0600); err != nil {
					t.Fatal(err)
				}
				f, err := privatefile.Open(file, 16384)
				if err != nil {
					t.Fatal("fixture lost its private access controls before tuple validation:", err)
				}
				_ = f.Close()
			}
			slug, days := "production", 30
			if change == "slug" {
				slug = "different"
			}
			if change == "retention" {
				days = 90
				_ = c.Flags().Set("retention-days", "90")
			}
			client := api.NewClient(srv.URL, "fixture", false)
			client.SetOrg(reviewedPipelineTestOrg)
			before, _ := os.ReadFile(file)
			if err := runClusterInstallLogCollector(c, context.Background(), client, slug, days); err == nil {
				t.Fatal("changed tuple admitted")
			}
			after, _ := os.ReadFile(file)
			if mutations != 0 || reviews != 0 || !bytes.Equal(before, after) {
				t.Fatal("refusal changed scope or request")
			}
		})
	}
}
func TestCollectorRecoveryOmittedRetentionUsesOriginal(t *testing.T) {
	c, _, file := collectorCommand(t)
	var input map[string]interface{}
	srv := agentInstallServer(t, func(q gqlRequest, w http.ResponseWriter) {
		data := collectorHTTPData()
		if strings.Contains(q.Query, "Review") {
			t.Error("recovery review")
		}
		if strings.Contains(q.Query, "mutation") {
			input = q.Variables["input"].(map[string]interface{})
			data["astroliftInstallClusterLogCollector"] = map[string]interface{}{"ok": true, "data": collectorResult(input, "QUEUED")}
		}
		writePipelineTestResponse(t, w, data)
	})
	defer srv.Close()
	r := collectorRequestFixture()
	r.Server = srv.URL
	r.RetentionDays = 90
	if err := createCollectorInstallRequest(file, r); err != nil {
		t.Fatal(err)
	}
	client := api.NewClient(srv.URL, "fixture", false)
	client.SetOrg(r.OrganizationID)
	if err := runClusterInstallLogCollector(c, context.Background(), client, "production", 30); err != nil {
		t.Fatal(err)
	}
	if input["retentionDays"] != float64(90) {
		t.Fatal("omitted retention changed original")
	}
}
func TestCollectorExactStatusTruthfulAndMismatchedProofRefused(t *testing.T) {
	for _, state := range []string{"QUEUED", "READER_GRANT_PENDING", "UNCERTAIN", "REFUSED", "ACTIVATED", "FOREIGN_ID", "BAD_PROOF", "UNKNOWN"} {
		t.Run(state, func(t *testing.T) {
			c, out, _ := collectorCommand(t)
			_ = c.Flags().Set("json", "true")
			srv := agentInstallServer(t, func(q gqlRequest, w http.ResponseWriter) {
				data := collectorHTTPData()
				op := collectorResult(map[string]interface{}{"clusterId": reviewedPipelineTestID, "requestId": agentInstallTestID, "expectedVersion": 7, "expectedSource": collectorTestSource, "retentionDays": 30}, state)
				if state == "ACTIVATED" {
					op["retryable"] = false
					op["postLossVerifiedAt"] = "2026-10-03T01:01:00Z"
					op["activatedAt"] = "2026-10-03T01:01:01Z"
					op["activatedClusterVersion"] = 8
				}
				if state == "FOREIGN_ID" {
					op["id"] = uuid.NewString()
					op["status"] = "QUEUED"
				}
				if state == "BAD_PROOF" {
					op["status"] = "ACTIVATED"
				}
				data["astroliftClusterLogCollectorOperation"] = op
				writePipelineTestResponse(t, w, data)
			})
			defer srv.Close()
			client := api.NewClient(srv.URL, "fixture", false)
			client.SetOrg(reviewedPipelineTestOrg)
			err := runClusterLogCollectorStatus(c, context.Background(), client, agentInstallTestID)
			bad := state == "FOREIGN_ID" || state == "BAD_PROOF" || state == "UNKNOWN"
			if bad {
				if err == nil || out.Len() != 0 {
					t.Fatalf("invalid proof printed: %v %s", err, out.String())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var op clusterCollectorOperation
			if json.Unmarshal(out.Bytes(), &op) != nil || op.Status != state {
				t.Fatal("status did not preserve exact metadata")
			}
		})
	}
}
func TestClusterInstallCapabilitiesUseAnonymousPublicEndpointAndFailClosed(t *testing.T) {
	for _, mode := range []string{"missing", "unavailable", "supported"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/app/gql/config/public/" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Astrolift-Organization") != "" {
					t.Error("wrong public handshake authority")
				}
				if mode == "unavailable" {
					w.WriteHeader(503)
					_, _ = w.Write([]byte("PRIVATE_BODY"))
					return
				}
				caps := []string{}
				if mode == "supported" {
					caps = append(caps, clusterCollectorCapability)
				}
				writePipelineTestResponse(t, w, map[string]interface{}{"astroliftServerInfo": map[string]interface{}{"capabilities": caps}})
			}))
			defer srv.Close()
			client := api.NewClient(srv.URL, "PRIVATE_BEARER", false)
			client.SetOrg(reviewedPipelineTestOrg)
			err := requireClusterInstallCapability(context.Background(), client, clusterCollectorCapability)
			if (err == nil) != (mode == "supported") || calls != 1 {
				t.Fatalf("preflight: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "PRIVATE_BODY") {
				t.Fatal("body leaked")
			}
		})
	}
}

func TestClusterInstallCommandsRefuseOldServerBeforeAuthenticatedRequests(t *testing.T) {
	for _, kind := range []string{"agent", "collector"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/app/gql/config/public/" {
					t.Error("unsupported server received authenticated target access")
				}
				writePipelineTestResponse(t, w, map[string]interface{}{"astroliftServerInfo": map[string]interface{}{"capabilities": []string{}}})
			}))
			defer srv.Close()
			c, _, file := collectorCommand(t)
			client := api.NewClient(srv.URL, "fixture", false)
			client.SetOrg(reviewedPipelineTestOrg)
			var err error
			if kind == "agent" {
				err = runClusterInstallAgent(c, context.Background(), client, "production", "", 0)
			} else {
				err = runClusterInstallLogCollector(c, context.Background(), client, "production", 30)
			}
			if err == nil || !strings.Contains(err.Error(), "does not advertise") || calls != 1 {
				t.Fatalf("old server not refused: %v", err)
			}
			if _, err := os.Stat(file); !os.IsNotExist(err) {
				t.Fatal("unsupported server produced request file")
			}
		})
	}
}

func TestCollectorMutationRefusesMissingAndMismatchedReceiptWithoutBodyDiagnostic(t *testing.T) {
	for _, fault := range []string{"missing", "refused", "cluster", "request", "version", "source", "retention", "status"} {
		t.Run(fault, func(t *testing.T) {
			c, out, file := collectorCommand(t)
			srv := agentInstallServer(t, func(q gqlRequest, w http.ResponseWriter) {
				data := collectorHTTPData()
				if strings.Contains(q.Query, "mutation") {
					op := collectorResult(q.Variables["input"].(map[string]interface{}), "QUEUED")
					ok := true
					switch fault {
					case "missing":
						op = nil
					case "refused":
						ok = false
					case "cluster":
						op["clusterId"] = uuid.NewString()
					case "request":
						op["requestId"] = uuid.NewString()
					case "version":
						op["expectedVersion"] = 8
					case "source":
						op["expectedSource"] = strings.Repeat("b", 64)
					case "retention":
						op["retentionDays"] = 90
					case "status":
						op["status"] = "UNRECOGNIZED"
					}
					data["astroliftInstallClusterLogCollector"] = map[string]interface{}{"ok": ok, "errors": []interface{}{map[string]interface{}{"message": "PRIVATE_MUTATION_BODY"}}, "data": op}
				}
				writePipelineTestResponse(t, w, data)
			})
			defer srv.Close()
			client := api.NewClient(srv.URL, "fixture", false)
			client.SetOrg(reviewedPipelineTestOrg)
			err := runClusterInstallLogCollector(c, context.Background(), client, "production", 30)
			if err == nil || strings.Contains(err.Error(), "PRIVATE_MUTATION_BODY") || out.Len() != 0 {
				t.Fatalf("invalid receipt published: %v %s", err, out.String())
			}
			if _, err := readCollectorInstallRequest(file); err != nil {
				t.Fatal("original durable request lost")
			}
		})
	}
}

func TestPublicInstallDiscoveryDoesNotFollowLoginGatewayRedirect(t *testing.T) {
	gatewayCalls := 0
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gatewayCalls++; w.WriteHeader(http.StatusUnauthorized) }))
	defer gateway.Close()
	sourceCalls := 0
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceCalls++
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Astrolift-Organization") != "" {
			t.Error("public discovery exposed credentials")
		}
		http.Redirect(w, r, gateway.URL+"/login?private=PRIVATE_GATEWAY_MARKER", http.StatusFound)
	}))
	defer source.Close()
	client := api.NewClient(source.URL, "PRIVATE_BEARER", false)
	client.SetOrg(reviewedPipelineTestOrg)
	err := requireClusterInstallCapability(context.Background(), client, clusterCollectorCapability)
	if err == nil || !strings.Contains(err.Error(), "ask the operator to expose") || strings.Contains(err.Error(), "PRIVATE_GATEWAY_MARKER") || sourceCalls != 1 || gatewayCalls != 0 {
		t.Fatalf("redirect was followed or unhelpfully reported: %v, %d/%d", err, sourceCalls, gatewayCalls)
	}
}
