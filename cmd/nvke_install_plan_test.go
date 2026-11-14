package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/nvkecatalog"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/spf13/cobra"
)

const nvkePlanTestID = "3e4e2355-cfe1-425f-a28b-64de032b84dc"

func nvkeInstallPlanBool(v bool) *bool { return &v }
func nvkeInstallPlanInt(v int) *int    { return &v }
func nvkeInstallPlanFixture() *api.NVKEInstallPlan {
	date := "2026-11-08T21:00:00Z"
	sha := strings.Repeat("a", 64)
	return &api.NVKEInstallPlan{ID: nvkePlanTestID, Version: 2, Status: "prepared", OperationID: nvkeTestOperation, OperationVersion: 4, SiteID: nvkeTestSite, SiteVersion: 7, AuthorityID: nvkeTestOperation, AuthorityVersion: 3, ItemCount: 2, RetainedBytes: "123", ProposedBytes: "456", Plan: api.NVKEInstallPlanHeader{Format: 1, Scope: "remaining-management-install-proposal", ID: nvkePlanTestID, PreparedAt: date, PhaseEvidence: "retained-native-stderr-order-only", RequiresOriginalSourceVerification: nvkeInstallPlanBool(true), RequiresExplicitAcknowledgement: nvkeInstallPlanBool(true), ContinuationAuthorized: nvkeInstallPlanBool(false), FirstRemainingOrdinal: nvkeInstallPlanInt(1), PlanDigest: strings.Repeat("b", 64), Source: api.NVKEInstallPlanSource{SiteID: nvkeTestSite, InstanceID: nvkeNativeTestSource, JobID: nvkeTestOperation, NativeRunID: nvkeTestOrg, NativeRevision: nvkecatalog.Pinned().Revision, NativeExitCode: 1, BundleManifestSHA256: sha, NativeArgumentsSHA256: sha, RetainedInputsSHA256: sha, ProposedInputsSHA256: sha, ProposedExecutionSHA256: sha, PrivateSourceSHA256: sha, ReviewDigest: sha, StderrSHA256: sha, OriginalStartedAt: date, ReviewObservedAt: date, OriginalDeploymentEvidence: "reconstructed-prepared-deployment"}}, Infrastructure: api.NVKEInstallPlanInfrastructure{APIReady: nvkeInstallPlanBool(true), ObservedAt: date, ClusterUID: nvkeTestOrg, WorkloadCount: nvkeInstallPlanInt(1)}}
}
func nvkeInstallPlanTestCommand(t *testing.T, verb string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd, _, err := newNVKEAgentCommand().Find([]string{"install-plan", verb})
	if err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetContext(context.Background())
	for k, v := range map[string]string{"site": nvkeTestSite, "site-version": "7", "authority-version": "3", "version": "4", "plan": nvkePlanTestID} {
		if err = cmd.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if verb == "items" || verb == "workloads" {
		_ = cmd.Flags().Set("plan-digest", strings.Repeat("b", 64))
	}
	return cmd, out
}
func nvkeInstallPlanTestData() map[string]any {
	data := agentTestData()
	data["astroliftNvkeOperation"] = map[string]any{"id": nvkeTestOperation, "version": 4, "siteId": nvkeTestSite, "requestId": nvkeNativeTestSource, "operationKey": "tooling.management.install", "status": "UNCERTAIN"}
	return data
}
func nvkeInstallPlanTestJSON(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err = json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
func TestNVKEInstallPlanSourceValidationAndNoExecution(t *testing.T) {
	for _, kind := range []string{"valid", "operation", "site", "authority", "instance", "revision", "successful-native", "missing-continuation", "continuation", "missing-ordinal", "bad-ordinal", "missing-ack", "bad-counter", "overflow", "cluster", "timestamp", "digest", "refused", "source-settled"} {
		t.Run(kind, func(t *testing.T) {
			cmd, out := nvkeInstallPlanTestCommand(t, "review")
			mutations := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				data := nvkeInstallPlanTestData()
				if kind == "source-settled" {
					data["astroliftNvkeOperation"].(map[string]any)["status"] = "SUCCEEDED"
				}
				if strings.HasPrefix(req.Query, "mutation") {
					mutations++
					if !strings.Contains(req.Query, "reviewNvkeOperatorInstallPlan(") || strings.Contains(req.Query, "requestNvkeOperatorJob") {
						t.Error("plan review admitted another intent")
					}
					input := req.Variables["input"].(map[string]any)
					if input["planId"] != nvkePlanTestID || input["expectedOperationVersion"] != float64(4) || input["expectedActorId"] != float64(42) {
						t.Error("lost reviewed identity")
					}
					row := nvkeInstallPlanFixture()
					switch kind {
					case "operation":
						row.OperationID = nvkeTestOrg
					case "site":
						row.SiteID = nvkeTestOrg
					case "authority":
						row.AuthorityID = nvkeTestOrg
					case "instance":
						row.Plan.Source.InstanceID = nvkeTestOrg
					case "revision":
						row.Plan.Source.NativeRevision = "invalid"
					case "successful-native":
						row.Plan.Source.NativeExitCode = 0
					case "missing-continuation":
						row.Plan.ContinuationAuthorized = nil
					case "continuation":
						row.Plan.ContinuationAuthorized = nvkeInstallPlanBool(true)
					case "missing-ordinal":
						row.Plan.FirstRemainingOrdinal = nil
					case "bad-ordinal":
						row.Plan.FirstRemainingOrdinal = nvkeInstallPlanInt(2)
					case "missing-ack":
						row.Plan.RequiresExplicitAcknowledgement = nil
					case "bad-counter":
						row.RetainedBytes = "01"
					case "overflow":
						row.ProposedBytes = "9223372036854775808"
					case "cluster":
						row.Infrastructure.ClusterUID = "PRIVATE"
					case "timestamp":
						row.Plan.PreparedAt = "2026-11-07T21:00:00Z"
					case "digest":
						row.Plan.Source.ProposedExecutionSHA256 = "PRIVATE"
					}
					projected := nvkeInstallPlanTestJSON(t, row)
					projected["privateNativeInput"] = "PRIVATE_MUST_NOT_PRINT"
					data["reviewNvkeOperatorInstallPlan"] = map[string]any{"ok": true, "data": projected}
					if kind == "refused" {
						data["reviewNvkeOperatorInstallPlan"] = map[string]any{"ok": false, "errors": []map[string]any{{"code": "SOURCE_CHANGED", "message": "PRIVATE"}}}
					}
				}
				writePipelineTestResponse(t, w, data)
			})
			defer server.Close()
			err := runNVKEAgentInstallPlan(cmd, context.Background(), nvkeTestClient(server), nvkeTestOperation, "review")
			if (err == nil) != (kind == "valid") || strings.Contains(out.String(), "PRIVATE") || (err != nil && strings.Contains(err.Error(), "PRIVATE")) {
				t.Fatalf("unsafe plan kind=%s err=%v", kind, err)
			}
			want := 1
			if kind == "source-settled" {
				want = 0
			}
			if mutations != want {
				t.Fatalf("mutations=%d want=%d", mutations, want)
			}
		})
	}
}
func TestNVKEInstallPlanPreparationJournalPrecedesMutationAndCannotChange(t *testing.T) {
	cmd, out := nvkeInstallPlanTestCommand(t, "prepare")
	journal := filepath.Join(t.TempDir(), "recovery.json")
	inputFile := filepath.Join(t.TempDir(), "input.json")
	body := `{"format":1,"planId":"` + nvkePlanTestID + `","request":null,"maximumInputBytes":"1000000","overrides":{"files":{"bounds.json":"PRIVATE_INPUT"}}}`
	if err := privatefile.Write(inputFile, []byte(body)); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Flags().Set("file", inputFile)
	_ = cmd.Flags().Set("request-file", journal)
	calls := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		data := nvkeInstallPlanTestData()
		if strings.HasPrefix(req.Query, "mutation") {
			calls++
			if !strings.Contains(req.Query, "prepareNvkeOperatorInstallPlan(") {
				t.Error("wrong preparation mutation")
			}
			b, err := privatefile.ReadFile(journal, 16384)
			if err != nil || bytes.Contains(b, []byte("PRIVATE_INPUT")) {
				t.Error("private recovery was not durable before I/O or exposed input")
			}
			input := req.Variables["input"].(map[string]any)
			if input["requestText"] != body {
				t.Error("private request changed")
			}
			data["prepareNvkeOperatorInstallPlan"] = map[string]any{"ok": true, "data": nvkeInstallPlanFixture()}
		}
		writePipelineTestResponse(t, w, data)
	})
	defer server.Close()
	client := nvkeTestClient(server)
	if err := runNVKEAgentInstallPlan(cmd, context.Background(), client, nvkeTestOperation, "prepare"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "PRIVATE_INPUT") {
		t.Fatal("private input printed")
	}
	if err := privatefile.Write(inputFile, []byte(strings.Replace(body, "1000000", "2000000", 1))); err != nil {
		t.Fatal(err)
	}
	if err := runNVKEAgentInstallPlan(cmd, context.Background(), client, nvkeTestOperation, "prepare"); err == nil || calls != 1 {
		t.Fatal("changed private intent was resubmitted")
	}
}
func nvkeInstallPlanTestItems() *api.NVKEInstallPlanPage[api.NVKEInstallPlanItem] {
	sha := strings.Repeat("a", 64)
	return &api.NVKEInstallPlanPage[api.NVKEInstallPlanItem]{PlanID: nvkePlanTestID, PlanDigest: strings.Repeat("b", 64), View: "items", TotalCount: nvkeInstallPlanInt(2), Items: []api.NVKEInstallPlanItem{{Name: "base", Type: "manifest", OriginalType: "manifest", ContentSHA256: sha, OriginalContentSHA256: sha, Ordinal: nvkeInstallPlanInt(0), OriginalObservation: "returned-before-next-observed-item", ProposedAction: "skip"}, {Name: "nktp", Type: "helm", OriginalType: "helm", ContentSHA256: sha, OriginalContentSHA256: sha, Ordinal: nvkeInstallPlanInt(1), OriginalObservation: "last-observed-in-unsuccessful-run", ProposedAction: "execute"}}}
}
func TestNVKEInstallPlanItemPageRejectsWrongDigestActionAndTruncation(t *testing.T) {
	for _, kind := range []string{"valid", "digest", "action", "ordinal", "duplicate", "truncated", "cursor"} {
		t.Run(kind, func(t *testing.T) {
			page := nvkeInstallPlanTestItems()
			switch kind {
			case "digest":
				page.PlanDigest = "a"
			case "action":
				page.Items[0].ProposedAction = "execute"
			case "ordinal":
				page.Items[1].Ordinal = nil
			case "duplicate":
				page.Items[1].Name = "base"
			case "truncated":
				page.Items = page.Items[:1]
			case "cursor":
				s := ""
				page.NextCursor = &s
			}
			err := validateNVKEPlanItems(page, nvkeInstallPlanFixture(), 25, "", true)
			if (err == nil) != (kind == "valid") {
				t.Fatalf("kind=%s err=%v", kind, err)
			}
		})
	}
}
func TestNVKEInstallPlanWorkloadPagePreservesPrecisionAndUnknowns(t *testing.T) {
	cmd, out := nvkeInstallPlanTestCommand(t, "workloads")
	gen := "9007199254740993"
	updated := "0"
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		data := nvkeInstallPlanTestData()
		if strings.Contains(req.Query, "reviewNvkeOperatorInstallPlan(") {
			data["reviewNvkeOperatorInstallPlan"] = map[string]any{"ok": true, "data": nvkeInstallPlanFixture()}
		}
		if strings.Contains(req.Query, "astroliftNvkeOperatorInstallPlanWorkloadPage(") {
			if req.Variables["digest"] != strings.Repeat("b", 64) || req.Variables["limit"] != float64(25) || req.Variables["after"] != nil {
				t.Error("page binding changed")
			}
			data["astroliftNvkeOperatorInstallPlanWorkloadPage"] = map[string]any{"ok": true, "data": api.NVKEInstallPlanPage[api.NVKEInstallWorkload]{PlanID: nvkePlanTestID, PlanDigest: strings.Repeat("b", 64), View: "workloads", TotalCount: nvkeInstallPlanInt(1), Items: []api.NVKEInstallWorkload{{Namespace: "system", Kind: "Deployment", Name: "controller", Generation: gen, DesiredReplicas: "1", UpdatedReplicas: &updated, Observation: "requires-review"}}}}
		}
		writePipelineTestResponse(t, w, data)
	})
	defer server.Close()
	if err := runNVKEAgentInstallPlan(cmd, context.Background(), nvkeTestClient(server), nvkeTestOperation, "workloads"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"9007199254740993"`) || !strings.Contains(out.String(), `"readyReplicas": null`) {
		t.Fatal("precision or null observation lost")
	}
}

func TestNVKEInstallPlanRetryUsesRetainedServerIntentWithoutPrivateInput(t *testing.T) {
	cmd, out := nvkeInstallPlanTestCommand(t, "retry-preparation")
	calls := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		data := nvkeInstallPlanTestData()
		if strings.HasPrefix(req.Query, "mutation") {
			calls++
			if !strings.Contains(req.Query, "recoverNvkeOperatorInstallPlanPreparation(") {
				t.Error("retry used another mutation")
			}
			in := req.Variables["input"].(map[string]any)
			if len(in) != 6 || in["planId"] != nvkePlanTestID {
				t.Error("retry changed retained intent")
			}
			data["recoverNvkeOperatorInstallPlanPreparation"] = map[string]any{"ok": true, "data": nvkeInstallPlanFixture()}
		}
		writePipelineTestResponse(t, w, data)
	})
	defer server.Close()
	if err := runNVKEAgentInstallPlan(cmd, context.Background(), nvkeTestClient(server), nvkeTestOperation, "retry-preparation"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !strings.Contains(out.String(), `"continuationAuthorized": false`) {
		t.Fatal("retry outcome invalid")
	}
}
func TestNVKEInstallPlanItemCommandBindsPageArguments(t *testing.T) {
	cmd, _ := nvkeInstallPlanTestCommand(t, "items")
	_ = cmd.Flags().Set("limit", "1")
	_ = cmd.Flags().Set("search", "nktp")
	_ = cmd.Flags().Set("after", "reviewed-cursor")
	pages := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		data := nvkeInstallPlanTestData()
		if strings.Contains(req.Query, "reviewNvkeOperatorInstallPlan(") {
			data["reviewNvkeOperatorInstallPlan"] = map[string]any{"ok": true, "data": nvkeInstallPlanFixture()}
		}
		if strings.Contains(req.Query, "astroliftNvkeOperatorInstallPlanItemPage(") {
			pages++
			if req.Variables["search"] != "nktp" || req.Variables["after"] != "reviewed-cursor" || req.Variables["limit"] != float64(1) {
				t.Error("page arguments changed")
			}
			page := nvkeInstallPlanTestItems()
			page.Items = page.Items[1:]
			page.TotalCount = nvkeInstallPlanInt(1)
			data["astroliftNvkeOperatorInstallPlanItemPage"] = map[string]any{"ok": true, "data": page}
		}
		writePipelineTestResponse(t, w, data)
	})
	defer server.Close()
	if err := runNVKEAgentInstallPlan(cmd, context.Background(), nvkeTestClient(server), nvkeTestOperation, "items"); err != nil {
		t.Fatal(err)
	}
	if pages != 1 {
		t.Fatal("missing item page")
	}
}
func TestNVKEInstallPlanLostReplyRetainsJournalAndReviewNeedsNoInput(t *testing.T) {
	cmd, _ := nvkeInstallPlanTestCommand(t, "prepare")
	journal := filepath.Join(t.TempDir(), "recovery.json")
	_ = cmd.Flags().Set("request-file", journal)
	_ = cmd.Flags().Set("file", "-")
	cmd.SetIn(strings.NewReader(`{"format":1,"planId":"` + nvkePlanTestID + `","request":null,"maximumInputBytes":"1000000"}`))
	prepareCalls, reviewCalls := 0, 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		data := nvkeInstallPlanTestData()
		if strings.Contains(req.Query, "prepareNvkeOperatorInstallPlan(") {
			prepareCalls++
			if _, err := privatefile.ReadFile(journal, 16384); err != nil {
				t.Error("journal missing before lost reply")
			}
			h, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("test transport cannot discard reply")
			}
			conn, _, err := h.Hijack()
			if err != nil {
				t.Fatal(err)
			}
			_ = conn.Close()
			return
		}
		if strings.Contains(req.Query, "reviewNvkeOperatorInstallPlan(") {
			reviewCalls++
			data["reviewNvkeOperatorInstallPlan"] = map[string]any{"ok": true, "data": nvkeInstallPlanFixture()}
		}
		writePipelineTestResponse(t, w, data)
	})
	defer server.Close()
	client := nvkeTestClient(server)
	if err := runNVKEAgentInstallPlan(cmd, context.Background(), client, nvkeTestOperation, "prepare"); err == nil || !strings.Contains(err.Error(), nvkePlanTestID) {
		t.Fatal("lost reply failed to retain plan identity")
	}
	review, _ := nvkeInstallPlanTestCommand(t, "review")
	if err := runNVKEAgentInstallPlan(review, context.Background(), client, nvkeTestOperation, "review"); err != nil {
		t.Fatal(err)
	}
	if prepareCalls != 1 || reviewCalls != 1 {
		t.Fatal("lost reply automatically resubmitted")
	}
}
func TestNVKEInstallPlanInvalidPageInputDoesNotContactPeer(t *testing.T) {
	for _, change := range []struct{ flag, value string }{{"limit", "0"}, {"search", strings.Repeat("x", 201)}, {"after", ""}, {"plan-digest", "bad"}} {
		t.Run(change.flag, func(t *testing.T) {
			cmd, _ := nvkeInstallPlanTestCommand(t, "items")
			_ = cmd.Flags().Set(change.flag, change.value)
			calls := 0
			server := nvkeTestServer(t, func(_ gqlRequest, _ http.ResponseWriter) { calls++ })
			defer server.Close()
			if err := runNVKEAgentInstallPlan(cmd, context.Background(), nvkeTestClient(server), nvkeTestOperation, "items"); err == nil || calls != 0 {
				t.Fatal("invalid local page contacted peer")
			}
		})
	}
}
