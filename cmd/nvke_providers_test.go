package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/nvkecatalog"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/spf13/cobra"
)

const providerTestConnection = "123e4567-e89b-12d3-a456-426614174091"

func providerTestCommand(t *testing.T, verb string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd, _, err := newNVKEProvidersCommand().Find([]string{verb})
	if err != nil {
		t.Fatal(err)
	}
	cmd.SetContext(context.Background())
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.Flags().Bool("json", true, "")
	flags := map[string]string{}
	if verb != "catalog" && verb != "recover" {
		flags["site"] = nvkeTestSite
	}
	if verb == "request" {
		flags["site-version"] = "7"
		flags["connection"] = providerTestConnection
		flags["connection-version"] = "3"
		flags["request-file"] = filepath.Join(t.TempDir(), "request.json")
	}
	if verb == "observation" || verb == "identities" {
		flags["version"] = "9"
	}
	for k, v := range flags {
		if err = cmd.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	return cmd, out
}
func providerTestSource() map[string]any {
	return map[string]any{"connectionId": providerTestConnection, "connectionVersion": 3, "siteVersion": 7, "upstreamRevision": nvkecatalog.Pinned().Revision}
}
func providerTestData() map[string]any {
	d := nvkeBaseData()
	d["astroliftNvkeSite"].(map[string]any)["connections"] = []map[string]any{{"id": providerTestConnection, "version": 3, "surface": "DATASTORE", "endpoint": "https://PRIVATE_MANAGER.test", "isEnabled": true}}
	return d
}
func providerTestReceipt(input map[string]interface{}) map[string]any {
	return map[string]any{"id": nvkeTestOperation, "siteId": nvkeTestSite, "requestId": input["requestId"], "operationKey": nvkeProviderOperationKey, "isInventory": false, "providerSource": providerTestSource(), "status": "SUCCEEDED", "version": 9}
}
func providerTestObservation() map[string]any {
	return map[string]any{"siteId": nvkeTestSite, "operationId": nvkeTestOperation, "operationVersion": 9, "sourceSiteVersion": 7, "connectionId": providerTestConnection, "connectionVersion": 3, "upstreamRevision": nvkecatalog.Pinned().Revision, "observedAt": "2026-11-08T12:00:00.123456789Z", "complete": true, "providers": []map[string]any{
		{"provider": "nico", "state": "DEGRADED", "reason": "provider_manager_unavailable", "complete": true, "crdsEstablished": true, "controllerAvailable": false, "managerNamespace": "capnico-system", "managerName": "nico-manager", "readyReplicas": "0", "desiredReplicas": "1", "secretNamespace": "capnico-system", "secretName": "nico-credentials", "secretPresent": true, "unconfiguredIsValid": false, "credentials": map[string]any{"health": "HEALTHY", "reason": "provider_check_succeeded", "identityNamespace": "capnico-system", "identityName": "a-selected", "identityGeneration": "9007199254740993", "observedGeneration": "9007199254740993", "readyStatus": "True", "lastCheckedTime": "2026-11-08T11:59:00.123456789Z", "additionalIdentityCount": 31}},
		{"provider": "launchlayer", "state": "NOT-INSTALLED", "reason": "provider_crds_not_established", "complete": true, "crdsEstablished": false, "unconfiguredIsValid": true},
	}}
}
func TestNVKEProviderCollectionPersistsBeforeAdmissionAndRecoversBeforeAuthorityReads(t *testing.T) {
	cmd, out := providerTestCommand(t, "request")
	file := resourceStringFlag(cmd, "request-file")
	writes, sites := 0, 0
	var accepted map[string]any
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		d := providerTestData()
		if strings.Contains(req.Query, "astroliftNvkeSite(") {
			sites++
			if accepted != nil {
				t.Error("recovery read changed authority")
			}
		}
		if strings.HasPrefix(req.Query, "mutation") {
			writes++
			input := req.Variables["input"].(map[string]interface{})
			saved, err := readNVKEProviderRecovery(file)
			if err != nil || input["requestId"] != saved.RequestID || input["connectionId"] != providerTestConnection || input["expectedConnectionVersion"] != float64(3) || input["expectedActorId"] != float64(42) {
				t.Error("original identity was not persisted/reviewed")
			}
			accepted = providerTestReceipt(input)
			http.Error(w, "PRIVATE_LOST_RESPONSE", http.StatusServiceUnavailable)
			return
		}
		if strings.Contains(req.Query, "astroliftNvkeRequest(") {
			d["astroliftNvkeRequest"] = accepted
		}
		writePipelineTestResponse(t, w, d)
	})
	c := nvkeTestClient(server)
	if err := runNVKEProviderRequest(cmd, cmd.Context(), c, false); err == nil || !strings.Contains(err.Error(), "preserve") {
		t.Fatalf("lost reply not retained: %v", err)
	}
	original, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(original), "PRIVATE") || strings.Contains(out.String(), "PRIVATE") {
		t.Fatal("native endpoint or response leaked")
	}
	info, _ := os.Stat(file)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal("recovery file not private")
	}
	recoverCmd, recovered := providerTestCommand(t, "recover")
	_ = recoverCmd.Flags().Set("request-file", file)
	if err = runNVKEProviderRequest(recoverCmd, recoverCmd.Context(), c, true); err != nil {
		t.Fatal(err)
	}
	if writes != 1 || sites != 1 || !strings.Contains(recovered.String(), nvkeTestOperation) {
		t.Fatal("receipt recovery re-admitted or lost identity")
	}
	now, _ := os.ReadFile(file)
	if !bytes.Equal(original, now) {
		t.Fatal("recovery file changed")
	}
}
func TestNVKEProviderUnknownRequestCannotChangeTuple(t *testing.T) {
	for _, flag := range []string{"site-version", "connection-version", "connection", "operation-timeout"} {
		t.Run(flag, func(t *testing.T) {
			cmd, _ := providerTestCommand(t, "request")
			writes := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				d := providerTestData()
				if strings.HasPrefix(req.Query, "mutation") {
					writes++
					http.Error(w, "lost", http.StatusServiceUnavailable)
					return
				}
				d["astroliftNvkeRequest"] = nil
				writePipelineTestResponse(t, w, d)
			})
			c := nvkeTestClient(server)
			if runNVKEProviderRequest(cmd, cmd.Context(), c, false) == nil {
				t.Fatal("expected lost reply")
			}
			v := "99"
			if flag == "connection" {
				v = nvkeTestOperation
			}
			_ = cmd.Flags().Set(flag, v)
			if runNVKEProviderRequest(cmd, cmd.Context(), c, false) == nil || writes != 1 {
				t.Fatal("changed intent admitted")
			}
		})
	}
}
func TestNVKEProviderReceiptSourceMustMatchFrozenRequest(t *testing.T) {
	for _, field := range []string{"connectionId", "connectionVersion", "siteVersion", "upstreamRevision"} {
		t.Run(field, func(t *testing.T) {
			cmd, out := providerTestCommand(t, "request")
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				d := providerTestData()
				if strings.HasPrefix(req.Query, "mutation") {
					row := providerTestReceipt(req.Variables["input"].(map[string]interface{}))
					row["providerSource"].(map[string]any)[field] = "PRIVATE_INVALID"
					d["requestNvkeProviderObservation"] = map[string]any{"ok": true, "errors": []any{}, "data": row}
				}
				writePipelineTestResponse(t, w, d)
			})
			if err := runNVKEProviderRequest(cmd, cmd.Context(), nvkeTestClient(server), false); err == nil || out.Len() != 0 {
				t.Fatal("mismatched source receipt accepted")
			}
		})
	}
}
func TestNVKEProviderViewsPreservePrecisionUnknownsAndServerPaging(t *testing.T) {
	for _, kind := range []string{"observation", "identities"} {
		t.Run(kind, func(t *testing.T) {
			cmd, out := providerTestCommand(t, kind)
			if kind == "identities" {
				_ = cmd.Flags().Set("search", "extra-030")
				_ = cmd.Flags().Set("after", "signed-cursor")
			}
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				d := nvkeBaseData()
				d["astroliftNvkeOperation"] = providerTestReceipt(map[string]interface{}{"requestId": nvkeTestOperation})
				d["astroliftNvkeProviderObservation"] = map[string]any{"ok": true, "errors": []any{}, "data": providerTestObservation()}
				if strings.Contains(req.Query, "astroliftNvkeProviderIdentitiesPage") {
					if req.Variables["search"] != "extra-030" || req.Variables["after"] != "signed-cursor" || req.Variables["actor"] != float64(42) {
						t.Error("page scope changed")
					}
					d["astroliftNvkeProviderIdentitiesPage"] = map[string]any{"ok": true, "errors": []any{}, "data": map[string]any{"siteId": nvkeTestSite, "operationId": nvkeTestOperation, "operationVersion": 9, "observedAt": "2026-11-08T12:00:00.123456789Z", "provider": "nico", "items": []map[string]any{{"key": "capnico-system/extra-030", "namespace": "capnico-system", "name": "extra-030"}}, "totalCount": 1}}
				}
				writePipelineTestResponse(t, w, d)
			})
			if err := runNVKEProviderView(cmd, cmd.Context(), nvkeTestClient(server), nvkeTestOperation, kind); err != nil {
				t.Fatal(err)
			}
			if kind == "observation" && (!strings.Contains(out.String(), `"9007199254740993"`) || !strings.Contains(out.String(), "123456789Z") || !strings.Contains(out.String(), `"controllerAvailable": false`) || !strings.Contains(out.String(), `"endpointConfigured": null`)) {
				t.Fatal("precision or unknowns changed")
			}
		})
	}
}
func TestNVKEProviderMalformedFramesProduceNoOutput(t *testing.T) {
	for _, change := range []string{"version", "timestamp", "duplicate-provider", "count", "health", "source", "missing-complete", "missing-optional-policy"} {
		t.Run(change, func(t *testing.T) {
			cmd, out := providerTestCommand(t, "observation")
			server := nvkeTestServer(t, func(_ gqlRequest, w http.ResponseWriter) {
				d := nvkeBaseData()
				d["astroliftNvkeOperation"] = providerTestReceipt(map[string]interface{}{"requestId": nvkeTestOperation})
				obs := providerTestObservation()
				p := obs["providers"].([]map[string]any)[0]
				switch change {
				case "version":
					obs["operationVersion"] = 10
				case "timestamp":
					obs["observedAt"] = "PRIVATE_NATIVE"
				case "duplicate-provider":
					obs["providers"].([]map[string]any)[1] = p
				case "count":
					p["readyReplicas"] = "9223372036854775808"
				case "health":
					p["credentials"].(map[string]any)["observedGeneration"] = "1"
				case "source":
					obs["connectionId"] = nvkeTestOperation
				case "missing-complete":
					delete(p, "complete")
				case "missing-optional-policy":
					delete(p, "unconfiguredIsValid")
				}
				d["astroliftNvkeProviderObservation"] = map[string]any{"ok": true, "errors": []any{}, "data": obs}
				writePipelineTestResponse(t, w, d)
			})
			if err := runNVKEProviderView(cmd, cmd.Context(), nvkeTestClient(server), nvkeTestOperation, "observation"); err == nil || out.Len() != 0 {
				t.Fatal("malformed provider data disclosed")
			}
		})
	}
}
func TestNVKEProviderRecoveryRejectsDuplicateAndPrivateFields(t *testing.T) {
	row := nvkeProviderRequest{reviewedStartRequest: reviewedStartRequest{Format: 1, Kind: nvkeProviderRequestKind, ActorUserID: 42, TargetID: nvkeTestSite, RequestID: nvkeTestOperation, Version: 7, Ref: nvkeProviderOperationKey, Revision: nvkecatalog.Pinned().Revision, IntervalSeconds: 600}, ConnectionID: providerTestConnection, ConnectionVersion: 3}
	b, _ := json.Marshal(row)
	for _, extra := range []string{`,"connectionVersion":9}`, `,"token":"PRIVATE"}`, `} {}`} {
		name := filepath.Join(t.TempDir(), "record.json")
		body := append(append([]byte{}, b[:len(b)-1]...), []byte(extra)...)
		if privatefile.Write(name, body) != nil {
			t.Fatal("write fixture")
		}
		if _, err := readNVKEProviderRecovery(name); err == nil {
			t.Fatal("ambiguous/private recovery accepted")
		}
	}
}
func TestNVKEProviderObservationValidationRejectsForeignHistory(t *testing.T) {
	var obs api.NVKEProviderObservation
	b, _ := json.Marshal(providerTestObservation())
	_ = json.Unmarshal(b, &obs)
	receipt := &api.NVKEOperation{ProviderSource: &api.NVKEProviderSource{ConnectionID: providerTestConnection, ConnectionVersion: 3, SiteVersion: 7, UpstreamRevision: nvkecatalog.Pinned().Revision}}
	if validateNVKEProviderObservation(&obs, receipt) != nil {
		t.Fatal("valid history refused")
	}
	receipt.ProviderSource.ConnectionVersion = 4
	if validateNVKEProviderObservation(&obs, receipt) == nil {
		t.Fatal("foreign connection history accepted")
	}
}

func TestNVKEProviderWaitRefusesChangedAdmittedSource(t *testing.T) {
	cmd, out := providerTestCommand(t, "request")
	_ = cmd.Flags().Set("wait", "true")
	_ = cmd.Flags().Set("poll-interval", "500ms")
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		d := providerTestData()
		if strings.HasPrefix(req.Query, "mutation") {
			row := providerTestReceipt(req.Variables["input"].(map[string]interface{}))
			row["status"] = "QUEUED"
			d["requestNvkeProviderObservation"] = map[string]any{"ok": true, "errors": []any{}, "data": row}
		}
		if strings.Contains(req.Query, "astroliftNvkeOperation(") {
			row := providerTestReceipt(map[string]interface{}{"requestId": nvkeTestOperation})
			saved, _ := readNVKEProviderRecovery(resourceStringFlag(cmd, "request-file"))
			row["requestId"] = saved.RequestID
			row["providerSource"].(map[string]any)["connectionVersion"] = 4
			d["astroliftNvkeOperation"] = row
		}
		writePipelineTestResponse(t, w, d)
	})
	if err := runNVKEProviderRequest(cmd, cmd.Context(), nvkeTestClient(server), false); err == nil || out.Len() != 0 {
		t.Fatal("wait accepted a changed provider source")
	}
}

func TestNVKEProviderTimestampsKeepNanosecondsAndRejectOverprecision(t *testing.T) {
	for _, value := range []string{"2026-11-08T12:00:00Z", "2026-11-08T12:00:00.123456789Z", "2026-11-08T12:00:00.123456789+01:30"} {
		if !validNVKEProviderDate(&value) {
			t.Fatal("valid native timestamp refused")
		}
	}
	for _, value := range []string{"2026-11-08T12:00:00.1234567890Z", "2026-11-08T12:00:00x123Z", "2026-11-08T12:00:00+24:00"} {
		if validNVKEProviderDate(&value) {
			t.Fatal("malformed native timestamp accepted")
		}
	}
}

func TestNVKEProviderIdentityPagesRejectInconsistentProjections(t *testing.T) {
	for _, change := range []string{"provider", "timestamp", "duplicate", "missing-continuation"} {
		t.Run(change, func(t *testing.T) {
			cmd, out := providerTestCommand(t, "identities")
			server := nvkeTestServer(t, func(_ gqlRequest, w http.ResponseWriter) {
				d := nvkeBaseData()
				d["astroliftNvkeOperation"] = providerTestReceipt(map[string]interface{}{"requestId": nvkeTestOperation})
				d["astroliftNvkeProviderObservation"] = map[string]any{"ok": true, "errors": []any{}, "data": providerTestObservation()}
				item := map[string]any{"key": "capnico-system/extra", "namespace": "capnico-system", "name": "extra"}
				page := map[string]any{"siteId": nvkeTestSite, "operationId": nvkeTestOperation, "operationVersion": 9, "observedAt": "2026-11-08T12:00:00.123456789Z", "provider": "nico", "items": []map[string]any{item}, "totalCount": 31, "nextCursor": "opaque"}
				switch change {
				case "provider":
					page["provider"] = "launchlayer"
				case "timestamp":
					page["observedAt"] = "2026-11-08T12:00:00.123456788Z"
				case "duplicate":
					page["items"] = []map[string]any{item, item}
				case "missing-continuation":
					page["nextCursor"] = nil
				}
				d["astroliftNvkeProviderIdentitiesPage"] = map[string]any{"ok": true, "errors": []any{}, "data": page}
				writePipelineTestResponse(t, w, d)
			})
			if err := runNVKEProviderView(cmd, cmd.Context(), nvkeTestClient(server), nvkeTestOperation, "identities"); err == nil || out.Len() != 0 {
				t.Fatal("inconsistent identity page disclosed")
			}
		})
	}
}
