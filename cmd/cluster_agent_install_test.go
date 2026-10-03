package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const agentInstallTestID = "00000000-0000-4000-8000-000000000091"
const agentInstallTestSource = "reviewed-cluster-provider-source"

func agentInstallServer(t *testing.T, fn func(gqlRequest, http.ResponseWriter)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request gqlRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("request decode: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		request.Organization = r.Header.Get("X-Astrolift-Organization")
		fn(request, w)
	}))
}

func agentInstallCommand(t *testing.T) (*cobra.Command, *bytes.Buffer, string) {
	t.Helper()
	c := &cobra.Command{}
	c.SetContext(context.Background())
	out := &bytes.Buffer{}
	c.SetOut(out)
	c.SetErr(out)
	file := filepath.Join(t.TempDir(), "agent-install.json")
	c.Flags().String("request-file", file, "")
	c.Flags().Bool("json", false, "")
	return c, out, file
}

func agentInstallData() map[string]interface{} {
	return map[string]interface{}{
		"astroliftMyProfile": map[string]interface{}{"userId": 42, "username": "operator"},
		"astroliftClusters": []interface{}{
			map[string]interface{}{"id": reviewedPipelineTestID, "slug": "production", "authMethod": "exec_plugin", "endpoint": "https://private-kubernetes.invalid"},
		},
		"astroliftClusterAgentInstallReview": map[string]interface{}{"clusterId": reviewedPipelineTestID, "version": 7, "source": agentInstallTestSource},
	}
}

func agentInstallResult(requestID, status string) map[string]interface{} {
	confirmed := status == "SUCCEEDED"
	return map[string]interface{}{
		"id": agentInstallTestID, "clusterId": reviewedPipelineTestID, "requestId": requestID,
		"status": status, "secretConfirmed": confirmed || status == "AWAITING_HEARTBEAT",
		"deploymentConfirmed": confirmed || status == "AWAITING_HEARTBEAT", "heartbeatConfirmed": confirmed,
		"retryable": false, "workflowId": "InstallClusterAgentWorkflow-" + agentInstallTestID,
		"errorCode": "", "errorMessage": "", "createdAt": "2026-10-03T00:00:00Z", "updatedAt": "2026-10-03T00:00:00Z",
	}
}

func TestClusterAgentInstallPersistsMetadataBeforeRemoteDispatch(t *testing.T) {
	c, out, file := agentInstallCommand(t)
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "missing-kubeconfig"))
	var mutationInput map[string]interface{}
	srv := agentInstallServer(t, func(q gqlRequest, w http.ResponseWriter) {
		if strings.Contains(q.Query, "issueClusterAgentKey") || strings.Contains(q.Query, "AuthDetails") || strings.Contains(q.Query, "agentKey") {
			t.Errorf("local or raw-key path called: %s", q.Query)
		}
		if q.Organization != reviewedPipelineTestOrg {
			t.Errorf("incorrect organization header: %s", q.Organization)
		}
		data := agentInstallData()
		if strings.Contains(q.Query, "mutation InstallClusterAgent") {
			mutationInput = q.Variables["input"].(map[string]interface{})
			r, err := readReviewedRequest(file)
			if err != nil || r.RequestID != mutationInput["requestId"] || r.IntervalSeconds != 17 || r.ExpectedSource != agentInstallTestSource {
				t.Errorf("request not durable before dispatch: %+v %v", r, err)
			}
			data["installClusterAgent"] = map[string]interface{}{"ok": true, "data": agentInstallResult(mutationInput["requestId"].(string), "QUEUED")}
		}
		writePipelineTestResponse(t, w, data)
	})
	defer srv.Close()
	client := api.NewClient(srv.URL, "PRIVATE_BEARER_FIXTURE", false)
	client.SetOrg(reviewedPipelineTestOrg)
	if err := runClusterInstallAgent(c, context.Background(), client, "production", "", 17); err != nil {
		t.Fatal(err)
	}
	if mutationInput["clusterId"] != reviewedPipelineTestID || mutationInput["expectedVersion"] != float64(7) || mutationInput["expectedSource"] != agentInstallTestSource || mutationInput["intervalSeconds"] != float64(17) {
		t.Fatalf("incorrect reviewed tuple: %+v", mutationInput)
	}
	f, err := privatefile.Open(file, 16384)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw)+out.String(), "PRIVATE_BEARER_FIXTURE") || strings.Contains(string(raw), "agentKey") {
		t.Fatal("request/output contains credential material")
	}
	if !strings.Contains(out.String(), "QUEUED") || !strings.Contains(out.String(), "heartbeat confirmed: false") || strings.Contains(out.String(), "Installed") {
		t.Fatalf("queued request claimed completion: %s", out.String())
	}
}

func TestClusterAgentInstallLostReplyRecoversSameTupleWithoutNewReview(t *testing.T) {
	c, _, file := agentInstallCommand(t)
	var inputs []map[string]interface{}
	reviews := 0
	srv := agentInstallServer(t, func(q gqlRequest, w http.ResponseWriter) {
		data := agentInstallData()
		if len(inputs) > 0 {
			// A provider-source change after dispatch must not refresh the
			// original review or replace its recovery identity on replay.
			data["astroliftClusterAgentInstallReview"] = map[string]interface{}{"clusterId": reviewedPipelineTestID, "version": 7, "source": "changed-provider-source"}
		}
		if strings.Contains(q.Query, "query ClusterAgentInstallReview") {
			reviews++
		}
		if strings.Contains(q.Query, "mutation InstallClusterAgent") {
			input := q.Variables["input"].(map[string]interface{})
			inputs = append(inputs, input)
			if len(inputs) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("PRIVATE_RESPONSE_MARKER"))
				return
			}
			data["installClusterAgent"] = map[string]interface{}{"ok": true, "data": agentInstallResult(input["requestId"].(string), "AWAITING_HEARTBEAT")}
		}
		writePipelineTestResponse(t, w, data)
	})
	defer srv.Close()
	client := api.NewClient(srv.URL, "fixture", false)
	client.SetOrg(reviewedPipelineTestOrg)
	err := runClusterInstallAgent(c, context.Background(), client, "production", "", 11)
	if err == nil || !strings.Contains(err.Error(), "outcome is unknown") || strings.Contains(err.Error(), "PRIVATE_RESPONSE_MARKER") {
		t.Fatalf("incorrect uncertain response: %v", err)
	}
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := runClusterInstallAgent(c, context.Background(), client, "production", "", 0); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(inputs[0])
	b, _ := json.Marshal(inputs[1])
	if reviews != 1 || inputs[0]["expectedSource"] != agentInstallTestSource || !bytes.Equal(a, b) || !bytes.Equal(before, after) {
		t.Fatalf("recovery replaced the reviewed identity: reviews=%d inputs=%s / %s", reviews, a, b)
	}
}

func TestClusterAgentInstallSavedScopeMismatchNeverDispatches(t *testing.T) {
	for _, change := range []string{"server", "org", "actor", "cluster", "interval", "version", "source"} {
		t.Run(change, func(t *testing.T) {
			c, _, file := agentInstallCommand(t)
			mutations := 0
			srv := agentInstallServer(t, func(q gqlRequest, w http.ResponseWriter) {
				if strings.Contains(q.Query, "mutation") {
					mutations++
				}
				writePipelineTestResponse(t, w, agentInstallData())
			})
			defer srv.Close()
			r := reviewedStartRequest{Format: 1, Kind: clusterAgentInstallKind, Server: srv.URL, OrganizationID: reviewedPipelineTestOrg, ActorUserID: 42, TargetID: reviewedPipelineTestID, TargetName: "production", Version: 7, RequestID: uuid.NewString(), IntervalSeconds: 10, ExpectedSource: agentInstallTestSource}
			switch change {
			case "server":
				r.Server = "https://other.invalid"
			case "org":
				r.OrganizationID = uuid.NewString()
			case "actor":
				r.ActorUserID = 43
			case "cluster":
				r.TargetName = "replacement"
			case "interval":
				r.IntervalSeconds = 12
			case "version":
				r.Version = 0
			case "source":
				r.ExpectedSource = ""
			}
			if err := createReviewedRequest(file, r); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(file)
			client := api.NewClient(srv.URL, "fixture", false)
			client.SetOrg(reviewedPipelineTestOrg)
			if err := runClusterInstallAgent(c, context.Background(), client, "production", "", 10); err == nil {
				t.Fatal("scope mismatch was accepted")
			}
			after, _ := os.ReadFile(file)
			if mutations != 0 || !bytes.Equal(before, after) {
				t.Fatal("mismatch dispatched or replaced saved identity")
			}
		})
	}
}

func TestClusterAgentInstallBadReviewCannotCreateRecoveryFileOrDispatch(t *testing.T) {
	for _, bad := range []interface{}{nil, map[string]interface{}{"clusterId": uuid.NewString(), "version": 7, "source": agentInstallTestSource}, map[string]interface{}{"clusterId": reviewedPipelineTestID, "version": 0, "source": agentInstallTestSource}, map[string]interface{}{"clusterId": reviewedPipelineTestID, "version": 7}, map[string]interface{}{"clusterId": reviewedPipelineTestID, "version": 7, "source": " "}} {
		c, _, file := agentInstallCommand(t)
		mutations := 0
		srv := agentInstallServer(t, func(q gqlRequest, w http.ResponseWriter) {
			if strings.Contains(q.Query, "mutation") {
				mutations++
			}
			data := agentInstallData()
			data["astroliftClusterAgentInstallReview"] = bad
			writePipelineTestResponse(t, w, data)
		})
		client := api.NewClient(srv.URL, "fixture", false)
		client.SetOrg(reviewedPipelineTestOrg)
		err := runClusterInstallAgent(c, context.Background(), client, "production", "", 0)
		srv.Close()
		if err == nil || mutations != 0 {
			t.Fatal("bad review dispatched")
		}
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatal("bad review left a request file")
		}
	}
}

func TestClusterAgentInstallLocalOverrideRefusedBeforeAPI(t *testing.T) {
	c, _, _ := agentInstallCommand(t)
	client := api.NewClient("http://127.0.0.1:1", "fixture", false)
	if err := runClusterInstallAgent(c, context.Background(), client, "production", "operator.yaml", 0); err == nil || !strings.Contains(err.Error(), "remove --kubeconfig") {
		t.Fatalf("local override reached API: %v", err)
	}
}

func TestClusterAgentInstallOldServerRefusesWithoutLocalKeyRotation(t *testing.T) {
	c, out, file := agentInstallCommand(t)
	mutations := 0
	srv := agentInstallServer(t, func(q gqlRequest, w http.ResponseWriter) {
		if strings.Contains(q.Query, "issueClusterAgentKey") || strings.Contains(q.Query, "AuthDetails") {
			t.Fatal("old server triggered local credential fallback")
		}
		if strings.Contains(q.Query, "mutation InstallClusterAgent") {
			mutations++
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"errors": []interface{}{map[string]interface{}{"message": `Cannot query field "installClusterAgent" on type "Mutation".`}},
			})
			return
		}
		writePipelineTestResponse(t, w, agentInstallData())
	})
	defer srv.Close()
	client := api.NewClient(srv.URL, "fixture", false)
	client.SetOrg(reviewedPipelineTestOrg)
	err := runClusterInstallAgent(c, context.Background(), client, "production", "", 0)
	if err == nil || mutations != 1 || out.Len() != 0 {
		t.Fatalf("unsupported mutation did not refuse cleanly: %v mutations=%d out=%s", err, mutations, out)
	}
	if _, err := readReviewedRequest(file); err != nil {
		t.Fatal("unsupported server lost the recovery identity")
	}
}

func TestClusterAgentInstallStatusReadDoesNotMutate(t *testing.T) {
	for _, status := range []string{"QUEUED", "INSTALLING", "AWAITING_HEARTBEAT", "SUCCEEDED"} {
		t.Run(status, func(t *testing.T) {
			c, out, _ := agentInstallCommand(t)
			_ = c.Flags().Set("json", "true")
			requestID := uuid.NewString()
			srv := agentInstallServer(t, func(q gqlRequest, w http.ResponseWriter) {
				if strings.Contains(q.Query, "mutation") || q.Variables["installId"] != agentInstallTestID {
					t.Errorf("status sent a mutation/wrong ID: %s", q.Query)
				}
				writePipelineTestResponse(t, w, map[string]interface{}{"astroliftClusterAgentInstall": agentInstallResult(requestID, status)})
			})
			defer srv.Close()
			client := api.NewClient(srv.URL, "fixture", false)
			client.SetOrg(reviewedPipelineTestOrg)
			if err := runClusterAgentInstallStatus(c, context.Background(), client, agentInstallTestID); err != nil {
				t.Fatal(err)
			}
			var got clusterAgentInstall
			if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Status != status || got.HeartbeatConfirmed != (status == "SUCCEEDED") {
				t.Fatalf("incorrect status/confirmation: %+v %v", got, err)
			}
		})
	}
}

func TestClusterAgentInstallRejectsUnconfirmedSuccessOrMismatchedReply(t *testing.T) {
	for _, change := range []string{"missing", "request", "cluster", "status", "heartbeat", "secret", "deployment"} {
		t.Run(change, func(t *testing.T) {
			requestID := uuid.NewString()
			data := agentInstallResult(requestID, "SUCCEEDED")
			switch change {
			case "missing":
				data = nil
			case "request":
				data["requestId"] = uuid.NewString()
			case "cluster":
				data["clusterId"] = uuid.NewString()
			case "status":
				data["status"] = "READY"
			case "heartbeat":
				data["heartbeatConfirmed"] = false
			case "secret":
				data["secretConfirmed"] = false
			case "deployment":
				data["deploymentConfirmed"] = false
			}
			var dto *clusterAgentInstall
			raw, _ := json.Marshal(data)
			_ = json.Unmarshal(raw, &dto)
			if err := checkClusterAgentInstall(dto, reviewedPipelineTestID, requestID); err == nil {
				t.Fatal("invalid completion/identity accepted")
			}
		})
	}
}

func TestClusterAgentInstallSuccessfulStatusShowsUnconfirmedRetirement(t *testing.T) {
	c, out, _ := agentInstallCommand(t)
	result := agentInstallResult(uuid.NewString(), "SUCCEEDED")
	result["errorCode"] = "RETIREMENT_UNCONFIRMED"
	result["errorMessage"] = "Replacement is active; retirement of the previous deployment is unconfirmed."
	srv := agentInstallServer(t, func(q gqlRequest, w http.ResponseWriter) {
		if strings.Contains(q.Query, "mutation") {
			t.Fatal("status attempted retirement or another mutation")
		}
		writePipelineTestResponse(t, w, map[string]interface{}{"astroliftClusterAgentInstall": result})
	})
	defer srv.Close()
	client := api.NewClient(srv.URL, "fixture", false)
	client.SetOrg(reviewedPipelineTestOrg)
	if err := runClusterAgentInstallStatus(c, context.Background(), client, agentInstallTestID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "heartbeat confirmed: true") || !strings.Contains(out.String(), "RETIREMENT_UNCONFIRMED") || !strings.Contains(out.String(), result["errorMessage"].(string)) {
		t.Fatalf("confirmed replacement hid pending retirement: %s", out.String())
	}
}
