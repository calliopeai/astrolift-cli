package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/nvkecatalog"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const nvkeTestSite = "123e4567-e89b-12d3-a456-426614174001"
const nvkeTestOrg = "123e4567-e89b-12d3-a456-426614174002"
const nvkeTestOperation = "123e4567-e89b-12d3-a456-426614174003"

func nvkeTestCommand(t *testing.T) (*cobra.Command, *bytes.Buffer, *bytes.Buffer, string) {
	t.Helper()
	cmd := newNVKERequestCommand("request ACTION_KEY", "")
	cmd.SetContext(context.Background())
	out, diagnostic := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(diagnostic)
	cmd.Flags().Bool("json", true, "")
	path := filepath.Join(t.TempDir(), "request.json")
	for name, value := range map[string]string{"site": nvkeTestSite, "request-file": path} {
		if err := cmd.Flags().Set(name, value); err != nil {
			t.Fatal(err)
		}
	}
	return cmd, out, diagnostic, path
}
func nvkeTestInput(t *testing.T, cmd *cobra.Command, body string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "input.json")
	if err := privatefile.Write(file, []byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("file", file); err != nil {
		t.Fatal(err)
	}
	return file
}
func nvkeTestServer(t *testing.T, handler func(gqlRequest, http.ResponseWriter)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/gql/config/" || r.Header.Get("Authorization") != "Bearer PRIVATE_NVKE_BEARER" || r.Header.Get("X-Astrolift-Organization") != nvkeTestOrg {
			t.Errorf("unsafe NVKE request scope: %s", r.URL.Path)
		}
		var request gqlRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		handler(request, w)
	}))
}
func nvkeBaseData() map[string]any {
	return map[string]any{
		"astroliftMyProfile":    map[string]any{"userId": 42, "username": "owner"},
		"astroliftNvkeSite":     map[string]any{"id": nvkeTestSite, "version": 7, "slug": "gpu-site", "isActive": true, "upstreamRevision": nvkecatalog.Pinned().Revision},
		"astroliftNvkeContract": map[string]any{"revision": nvkecatalog.Pinned().Revision, "actions": []map[string]any{{"key": "datastore.createMachineImage"}, {"key": "listClusters"}}},
	}
}
func nvkeTestResult(input map[string]interface{}, status string) map[string]any {
	return map[string]any{"id": nvkeTestOperation, "siteId": input["siteId"], "requestId": input["requestId"], "operationKey": input["operationKey"], "status": status, "version": 1, "stage": "queued"}
}
func nvkeTestClient(server *httptest.Server) *api.Client {
	c := api.NewClient(server.URL, "PRIVATE_NVKE_BEARER", false)
	c.SetOrg(nvkeTestOrg)
	return c
}
func TestNVKEDurableAdmissionAndLostReplyRecoverTheOriginalInput(t *testing.T) {
	cmd, out, diagnostics, file := nvkeTestCommand(t)
	nvkeTestInput(t, cmd, `{"document":{"metadata":{"name":"image-a"},"spec":{"futureField":{"int":9007199254740993,"private":"PRIVATE_BOOT_SCRIPT"}}}}`)
	inputs := []map[string]interface{}{}
	reviews := 0
	server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
		data := nvkeBaseData()
		if strings.Contains(request.Query, "astroliftNvkeSite(") {
			reviews++
		}
		if strings.Contains(request.Query, "requestNvkeOperation(") {
			input := request.Variables["input"].(map[string]interface{})
			inputs = append(inputs, input)
			saved, err := readReviewedRequest(file)
			if err != nil || saved.RequestID != input["requestId"] || saved.Version != 7 || saved.TargetID != input["siteId"] {
				t.Errorf("admission preceded durable recovery: %v", err)
			}
			if len(inputs) == 1 {
				w.WriteHeader(503)
				_, _ = io.WriteString(w, "PRIVATE_RESPONSE")
				return
			}
			data["requestNvkeOperation"] = map[string]any{"ok": true, "data": nvkeTestResult(input, "AWAITING_READY")}
		}
		writePipelineTestResponse(t, w, data)
	})
	defer server.Close()
	client := nvkeTestClient(server)
	if err := runNVKELegacyRequest(cmd, context.Background(), client, "datastore.createMachineImage", false); err == nil || !strings.Contains(err.Error(), "preserve") || strings.Contains(err.Error(), "PRIVATE_RESPONSE") {
		t.Fatalf("unsafe lost response: %v", err)
	}
	before, err := privatefile.ReadFile(file, 16384)
	if err != nil {
		t.Fatal(err)
	}
	if err := runNVKELegacyRequest(cmd, context.Background(), client, "datastore.createMachineImage", false); err != nil {
		t.Fatal(err)
	}
	after, _ := privatefile.ReadFile(file, 16384)
	if len(inputs) != 2 || reviews != 1 || !reflect.DeepEqual(inputs[0], inputs[1]) || !bytes.Equal(before, after) {
		t.Fatal("retry changed original admission tuple")
	}
	if strings.Contains(string(before)+out.String()+diagnostics.String(), "PRIVATE_") {
		t.Fatal("secret entered metadata or recovery record")
	}
	if !strings.Contains(out.String(), "AWAITING_READY") {
		t.Fatal("accepted operation reported as completed")
	}
}
func TestNVKERecoveryRefusesChangedInputScopeOrExecutionTuple(t *testing.T) {
	for _, change := range []string{"actor", "organization", "server", "site", "key", "parameters", "version", "timeout", "partial", "unknownfield"} {
		t.Run(change, func(t *testing.T) {
			cmd, _, _, file := nvkeTestCommand(t)
			mutations := 0
			server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
				data := nvkeBaseData()
				if change == "actor" {
					data["astroliftMyProfile"] = map[string]any{"userId": 43, "username": "other"}
				}
				if strings.Contains(request.Query, "mutation") {
					mutations++
				}
				writePipelineTestResponse(t, w, data)
			})
			defer server.Close()
			client := nvkeTestClient(server)
			saved := reviewedStartRequest{Format: 1, Kind: nvkeRequestKind, Server: server.URL, OrganizationID: nvkeTestOrg, ActorUserID: 42, TargetID: nvkeTestSite, TargetName: "site", RequestID: uuid.NewString(), Version: 7, Ref: "listClusters", Revision: nvkecatalog.Pinned().Revision, IntervalSeconds: 7200, ExpectedSource: nvkeParameterDigest([]byte("{}"))}
			key := "listClusters"
			switch change {
			case "organization":
				saved.OrganizationID = uuid.NewString()
			case "server":
				saved.Server = "https://other.test"
			case "site":
				saved.TargetID = uuid.NewString()
			case "key":
				key = "getCluster"
			case "parameters":
				nvkeTestInput(t, cmd, `{"query":{"name":"PRIVATE_CHANGED"}}`)
			case "version":
				_ = cmd.Flags().Set("site-version", "8")
			case "timeout":
				_ = cmd.Flags().Set("operation-timeout", "60")
			}
			if err := createReviewedRequest(file, saved); err != nil {
				t.Fatal(err)
			}
			if change == "partial" {
				if err := privatefile.Write(file, []byte(`{"format":`)); err != nil {
					t.Fatal(err)
				}
			}
			if change == "unknownfield" {
				raw, _ := privatefile.ReadFile(file, 16384)
				var value map[string]any
				_ = json.Unmarshal(raw, &value)
				value["privateUnexpected"] = "PRIVATE_CHANGED"
				raw, _ = json.Marshal(value)
				if err := privatefile.Write(file, raw); err != nil {
					t.Fatal(err)
				}
			}
			if err := runNVKELegacyRequest(cmd, context.Background(), client, key, false); err == nil || strings.Contains(err.Error(), "PRIVATE_CHANGED") {
				t.Fatalf("changed tuple accepted or leaked: %v", err)
			}
			if mutations != 0 {
				t.Fatal("changed tuple reached admission")
			}
		})
	}
}
func TestNVKERejectedInputDoesNotCreateRecoveryOrContactServer(t *testing.T) {
	for _, raw := range []string{`[]`, `null`, `{"private":"PRIVATE"} {}`, `{"private":"PRIVATE"`, strings.Repeat(" ", int(nvkeInputLimit)+1)} {
		t.Run(raw[:min(len(raw), 20)], func(t *testing.T) {
			cmd, _, _, file := nvkeTestCommand(t)
			nvkeTestInput(t, cmd, raw)
			requests := 0
			server := nvkeTestServer(t, func(_ gqlRequest, _ http.ResponseWriter) { requests++ })
			defer server.Close()
			if err := runNVKELegacyRequest(cmd, context.Background(), nvkeTestClient(server), "listClusters", false); err == nil || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatalf("unsafe invalid input: %v", err)
			}
			if requests != 0 {
				t.Fatal("invalid input reached server")
			}
			if _, err := os.Stat(file); !os.IsNotExist(err) {
				t.Fatal("invalid input created recovery file")
			}
		})
	}
}
func TestNVKEParameterJSONPreservesOpaqueIntegersAndAllNativeFields(t *testing.T) {
	cmd, _, _, _ := nvkeTestCommand(t)
	cmd.SetIn(strings.NewReader(`{"integer":9007199254740993,"futureField":{"x":[true,false,null,"secret"]}}`))
	data, err := readNVKEJSON(cmd, "-", nvkeInputLimit)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("9007199254740993")) || !bytes.Contains(data, []byte("futureField")) {
		t.Fatalf("native data lost: %s", data)
	}
}
func TestNVKEFullCatalogCommandsAreReachableWithoutNetwork(t *testing.T) {
	cmd := newNVKECommand()
	seen := map[string]bool{}
	for _, action := range nvkecatalog.Pinned().Actions {
		key := strings.TrimPrefix(action.Key, action.Surface+".")
		leaf, args, err := cmd.Find([]string{action.Surface, nativeNVKEName(key)})
		if err != nil || len(args) != 0 || leaf.RunE == nil || leaf.Flags().Lookup("request-file") == nil {
			t.Fatalf("missing native action %s", action.Key)
		}
		if seen[action.Key] {
			t.Fatal("duplicate action")
		}
		seen[action.Key] = true
	}
	if len(seen) != 88 {
		t.Fatalf("native surface count changed: %d", len(seen))
	}
	for _, path := range [][]string{{"site", "configure"}, {"site", "create"}, {"operation", "wait"}, {"operation", "result"}, {"operation", "resume"}, {"operation", "cancel"}, {"operation", "request"}} {
		leaf, args, err := cmd.Find(path)
		if err != nil || len(args) != 0 || leaf.RunE == nil {
			t.Fatalf("missing path %v", path)
		}
	}
}
func TestNVKEWaitTreatsReadinessAndUncertaintyTruthfully(t *testing.T) {
	for _, status := range []string{"SUCCEEDED", "FAILED", "CANCELLED", "UNCERTAIN", "UNKNOWN"} {
		t.Run(status, func(t *testing.T) {
			cmd, out, _, _ := nvkeTestCommand(t)
			row := &api.NVKEOperation{ID: nvkeTestOperation, SiteID: nvkeTestSite, OperationKey: "listClusters", RequestID: uuid.NewString(), Status: status}
			server := nvkeTestServer(t, func(_ gqlRequest, _ http.ResponseWriter) { t.Fatal("terminal wait contacted server") })
			defer server.Close()
			err := waitNVKEOperation(cmd, context.Background(), nvkeTestClient(server), row)
			if (err == nil) != (status == "SUCCEEDED") {
				t.Fatalf("status %s gave %v", status, err)
			}
			if status != "UNKNOWN" && !strings.Contains(out.String(), status) {
				t.Fatal("missing final metadata")
			}
		})
	}
	cmd, _, _, _ := nvkeTestCommand(t)
	_ = cmd.Flags().Set("wait-timeout", "1ms")
	row := &api.NVKEOperation{ID: nvkeTestOperation, Status: "AWAITING_READY"}
	server := nvkeTestServer(t, func(_ gqlRequest, _ http.ResponseWriter) { t.Fatal("local timeout issued a request") })
	defer server.Close()
	if err := waitNVKEOperation(cmd, context.Background(), nvkeTestClient(server), row); err == nil || !strings.Contains(err.Error(), "not been cancelled") {
		t.Fatalf("timeout: %v", err)
	}
}
func TestNVKEWaitDeadlineDuringAnInflightReadRetainsTheRemoteOperation(t *testing.T) {
	cmd, _, _, _ := nvkeTestCommand(t)
	_ = cmd.Flags().Set("wait-timeout", "2s")
	_ = cmd.Flags().Set("poll-interval", "500ms")
	reads := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		var body gqlRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || !strings.HasPrefix(strings.TrimSpace(body.Query), "query") {
			t.Error("waiting submitted something other than an observation")
		}
		reads <- struct{}{}
		<-request.Context().Done()
	}))
	defer server.Close()
	row := &api.NVKEOperation{ID: nvkeTestOperation, SiteID: nvkeTestSite, Status: "RUNNING"}
	err := waitNVKEOperation(cmd, context.Background(), nvkeTestClient(server), row)
	if err == nil || !strings.Contains(err.Error(), "not been cancelled") || !strings.Contains(err.Error(), row.ID) || len(reads) != 1 {
		t.Fatalf("in-flight local deadline lost the retained operation: %v", err)
	}
}

func TestNVKESensitiveResultRequiresExplicitOutputAndNeverOverwrites(t *testing.T) {
	requests := 0
	server := nvkeTestServer(t, func(_ gqlRequest, w http.ResponseWriter) {
		requests++
		writePipelineTestResponse(t, w, map[string]any{"astroliftNvkeOperationResultText": map[string]any{"ok": true, "data": `{"kubeconfig":"PRIVATE_KUBECONFIG"}`}})
	})
	defer server.Close()
	client := nvkeTestClient(server)
	cmd := &cobra.Command{}
	cmd.Flags().String("output", "", "")
	cmd.Flags().Bool("json", true, "")
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	if err := runNVKEOperation(cmd, context.Background(), client, nvkeTestOperation, "result"); err == nil || requests != 0 {
		t.Fatal("missing output fetched sensitive data")
	}
	file := filepath.Join(t.TempDir(), "result.json")
	_ = cmd.Flags().Set("output", file)
	if err := runNVKEOperation(cmd, context.Background(), client, nvkeTestOperation, "result"); err != nil {
		t.Fatal(err)
	}
	saved, err := privatefile.ReadFile(file, 1024)
	if err != nil || !bytes.Contains(saved, []byte("PRIVATE_KUBECONFIG")) {
		t.Fatalf("private result lost: %v", err)
	}
	if strings.Contains(out.String(), "PRIVATE") {
		t.Fatal("result leaked to ordinary output")
	}
	if err := runNVKEOperation(cmd, context.Background(), client, nvkeTestOperation, "result"); err == nil {
		t.Fatal("result overwrote original file")
	}
}
func TestNVKEWaitObservesRuntimeAndNeverCancelsOrReplays(t *testing.T) {
	cmd, out, _, _ := nvkeTestCommand(t)
	_ = cmd.Flags().Set("poll-interval", "500ms")
	_ = cmd.Flags().Set("wait-timeout", "2s")
	row := &api.NVKEOperation{ID: nvkeTestOperation, SiteID: nvkeTestSite, RequestID: uuid.NewString(), OperationKey: "updateCluster", Status: "AWAITING_READY"}
	polls := 0
	server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
		if strings.Contains(request.Query, "mutation") {
			t.Error("wait mutated infrastructure")
		}
		polls++
		next := *row
		next.Status = "SUCCEEDED"
		writePipelineTestResponse(t, w, map[string]any{"astroliftNvkeOperation": next})
	})
	defer server.Close()
	start := time.Now()
	if err := waitNVKEOperation(cmd, context.Background(), nvkeTestClient(server), row); err != nil {
		t.Fatal(err)
	}
	if polls != 1 || time.Since(start) < 450*time.Millisecond || !strings.Contains(out.String(), "SUCCEEDED") {
		t.Fatal("wait skipped runtime observation")
	}
}
