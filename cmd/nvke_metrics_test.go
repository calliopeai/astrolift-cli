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

	"github.com/calliopeai/astrolift-cli/internal/nvkecatalog"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/spf13/cobra"
)

func metricsTestCommand(t *testing.T, verb string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd, _, err := newNVKEMetricsCommand().Find([]string{verb})
	if err != nil {
		t.Fatal(err)
	}
	cmd.SetContext(context.Background())
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	values := map[string]string{}
	if verb != "recover" && verb != "catalog" {
		values["site"] = nvkeTestSite
	}
	if verb == "configure" || verb == "verify" || verb == "enable" || verb == "disable" || verb == "remove" {
		values["site-version"] = "7"
		values["authority-version"] = "3"
	}
	if verb == "configure" {
		values["endpoint"] = "https://metrics.test/private-metrics"
	}
	if verb == "request" {
		values["site-version"] = "7"
		values["authority-version"] = "3"
		values["source-operation"] = nvkeNativeTestSource
		values["source-version"] = "9"
		values["request-file"] = filepath.Join(t.TempDir(), "recovery.json")
	}
	if verb == "observation" || verb == "samples" || verb == "certificates" {
		values["version"] = "9"
	}
	for key, value := range values {
		if err = cmd.Flags().Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	return cmd, out
}

func metricsTestMetadata(verb string) map[string]any {
	row := map[string]any{"id": nvkeTestOperation, "version": 4, "siteId": nvkeTestSite, "siteVersion": 8, "endpoint": "https://metrics.test/private-metrics", "timeoutSeconds": 30, "hasCredentials": true, "isEnabled": verb == "configure" || verb == "enable" || verb == "verify" || verb == "get"}
	if verb == "get" {
		row["version"], row["siteVersion"] = 3, 7
	}
	if verb == "verify" {
		row["version"], row["siteVersion"], row["verificationCode"], row["verifiedAt"] = 3, 7, "METRICS_READ", "2026-11-08T00:00:00Z"
	}
	if verb == "remove" {
		row["deletedAt"] = "2026-11-08T00:00:00Z"
	}
	return row
}

func TestNVKEMetricsAuthorityWritesPreserveScopeAndNeverEchoCredentials(t *testing.T) {
	for _, verb := range []string{"configure", "verify", "enable", "disable", "remove"} {
		t.Run(verb, func(t *testing.T) {
			cmd, out := metricsTestCommand(t, verb)
			if verb == "configure" {
				_ = cmd.Flags().Set("credentials-file", "-")
				cmd.SetIn(strings.NewReader(`{"token":"PRIVATE_METRICS_BEARER","caPem":"PRIVATE_METRICS_CA"}`))
			}
			writes := 0
			server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
				data := nvkeBaseData()
				if strings.HasPrefix(request.Query, "mutation") {
					writes++
					input := request.Variables["input"].(map[string]interface{})
					if input["siteId"] != nvkeTestSite || input["expectedActorId"] != float64(42) || input["expectedSiteVersion"] != float64(7) || input["expectedVersion"] != float64(3) {
						t.Error("reviewed metrics scope changed")
					}
					field := map[string]string{"configure": "configureNvkeMetricsAuthority", "verify": "verifyNvkeMetricsAuthority", "enable": "setNvkeMetricsAuthorityEnabled", "disable": "setNvkeMetricsAuthorityEnabled", "remove": "removeNvkeMetricsAuthority"}[verb]
					if verb == "configure" && input["credentials"].(map[string]interface{})["token"] != "PRIVATE_METRICS_BEARER" {
						t.Error("wrong explicit metrics credential")
					}
					if (verb == "enable" || verb == "disable") && input["enabled"] != (verb == "enable") {
						t.Error("wrong enabled state")
					}
					row := metricsTestMetadata(verb)
					row["token"] = "PRIVATE_BAD_SERVER_ECHO"
					data[field] = map[string]any{"ok": true, "data": row}
				}
				writePipelineTestResponse(t, w, data)
			})
			defer server.Close()
			var err error
			if verb == "configure" {
				err = runNVKEMetricsConfigure(cmd, context.Background(), nvkeTestClient(server), nil)
			} else {
				err = runNVKEMetricsReview(cmd, context.Background(), nvkeTestClient(server), verb)
			}
			if err != nil || writes != 1 || strings.Contains(out.String(), "PRIVATE") {
				t.Fatalf("unsafe authority write: writes=%d err=%v output=%s", writes, err, out.String())
			}
		})
	}
}

func TestNVKEMetricsCredentialsArePreservedClearedOrStrictlyRejected(t *testing.T) {
	for _, mode := range []string{"preserve", "clear", "null", "unknown", "unpaired", "trailing", "conflicting", "public-file", "embedded-url", "http-without-opt-in"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "public-file" && runtime.GOOS == "windows" {
				t.Skip("POSIX mode check; Windows ACL checks live in internal/privatefile")
			}
			cmd, out := metricsTestCommand(t, "configure")
			if mode == "clear" {
				_ = cmd.Flags().Set("clear-credentials", "true")
			}
			bodies := map[string]string{"null": `{"token":null}`, "unknown": `{"apiKey":"PRIVATE"}`, "unpaired": `{"clientKeyPem":"PRIVATE"}`, "trailing": `{"token":"PRIVATE"}{}`, "conflicting": `{"token":"PRIVATE"}`, "public-file": `{"token":"PRIVATE"}`}
			if body, ok := bodies[mode]; ok {
				_ = cmd.Flags().Set("credentials-file", "-")
				cmd.SetIn(strings.NewReader(body))
			}
			if mode == "conflicting" {
				_ = cmd.Flags().Set("clear-credentials", "true")
			}
			if mode == "embedded-url" {
				_ = cmd.Flags().Set("endpoint", "https://PRIVATE:SECRET@metrics.test/path")
			}
			if mode == "http-without-opt-in" {
				_ = cmd.Flags().Set("endpoint", "http://metrics.test/path")
			}
			if mode == "public-file" {
				file := filepath.Join(t.TempDir(), "public.json")
				if err := os.WriteFile(file, []byte(bodies[mode]), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(file, 0644); err != nil {
					t.Fatal(err)
				}
				_ = cmd.Flags().Set("credentials-file", file)
			}
			writes := 0
			server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
				data := nvkeBaseData()
				if strings.HasPrefix(request.Query, "mutation") {
					writes++
					input := request.Variables["input"].(map[string]interface{})
					value, present := input["credentials"]
					if (mode == "preserve" && present) || (mode == "clear" && (!present || len(value.(map[string]interface{})) != 0)) {
						t.Error("preserve/clear distinction lost")
					}
					row := metricsTestMetadata("configure")
					row["hasCredentials"] = mode != "clear"
					data["configureNvkeMetricsAuthority"] = map[string]any{"ok": true, "data": row}
				}
				writePipelineTestResponse(t, w, data)
			})
			defer server.Close()
			err := runNVKEMetricsConfigure(cmd, context.Background(), nvkeTestClient(server), nil)
			valid := mode == "preserve" || mode == "clear"
			if valid && (err != nil || writes != 1) {
				t.Fatalf("valid update refused: %v", err)
			}
			if !valid && (err == nil || writes != 0 || strings.Contains(err.Error(), "PRIVATE")) {
				t.Fatalf("unsafe credential admission: %v writes=%d", err, writes)
			}
			if strings.Contains(out.String(), "PRIVATE") {
				t.Error("private credential escaped")
			}
		})
	}
}

func metricsRequestTestData() map[string]any {
	data := nvkeBaseData()
	data["astroliftNvkeMetricsAuthority"] = metricsTestMetadata("get")
	data["astroliftNvkeOperation"] = map[string]any{"id": nvkeNativeTestSource, "version": 9, "siteId": nvkeTestSite, "operationKey": "getCluster", "status": "SUCCEEDED"}
	data["astroliftNvkeRequest"] = nil
	return data
}

func metricsTestReceipt(input map[string]interface{}) map[string]any {
	return map[string]any{"id": nvkeTestOperation, "version": 1, "siteId": input["siteId"], "requestId": input["requestId"], "operationKey": nvkeCredentialOperationKey, "status": "QUEUED"}
}

func TestNVKECredentialLostReplyRecoversBeforeChangedAuthorityOrReview(t *testing.T) {
	cmd, out := metricsTestCommand(t, "request")
	file := resourceStringFlag(cmd, "request-file")
	var accepted map[string]any
	writes, authorityReads := 0, 0
	server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
		data := metricsRequestTestData()
		if strings.Contains(request.Query, "astroliftNvkeMetricsAuthority(") {
			authorityReads++
		}
		if accepted != nil {
			data["astroliftNvkeRequest"] = accepted
			delete(data, "astroliftNvkeMetricsAuthority")
			delete(data, "astroliftNvkeOperation")
		}
		if strings.Contains(request.Query, "requestNvkeCredentialObservation(") {
			writes++
			input := request.Variables["input"].(map[string]interface{})
			saved, err := readNVKECredentialRecovery(file)
			if err != nil || saved.RequestID != input["requestId"] || saved.SourceOperationID != nvkeNativeTestSource || saved.SourceVersion != 9 || saved.AuthorityVersion != 3 || input["expectedActorId"] != float64(42) || input["expectedSiteVersion"] != float64(7) || input["timeoutSeconds"] != float64(600) {
				t.Error("admission preceded durable exact review")
			}
			accepted = metricsTestReceipt(input)
			w.WriteHeader(503)
			_, _ = w.Write([]byte("PRIVATE_LOST_REPLY"))
			return
		}
		writePipelineTestResponse(t, w, data)
	})
	defer server.Close()
	c := nvkeTestClient(server)
	if err := runNVKECredentialRequest(cmd, context.Background(), c, false); err == nil || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatalf("lost reply must retain recovery: %v", err)
	}
	before, err := privatefile.ReadFile(file, 16384)
	if err != nil {
		t.Fatal(err)
	}
	_ = cmd.Flags().Set("authority-version", "99")
	_ = cmd.Flags().Set("source-operation", nvkeTestOperation)
	if err = runNVKECredentialRequest(cmd, context.Background(), c, false); err != nil {
		t.Fatal(err)
	}
	recoverCmd, recoverOut := metricsTestCommand(t, "recover")
	_ = recoverCmd.Flags().Set("request-file", file)
	if err = runNVKECredentialRequest(recoverCmd, context.Background(), c, true); err != nil {
		t.Fatal(err)
	}
	after, err := privatefile.ReadFile(file, 16384)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || writes != 1 || authorityReads != 1 || strings.Contains(out.String()+recoverOut.String(), "PRIVATE") {
		t.Fatal("recovery changed original identity or rereviewed authority")
	}
}

func TestNVKECredentialMissingReceiptCannotChangePendingTuple(t *testing.T) {
	for _, flag := range []string{"site-version", "authority-version", "source-version", "operation-timeout", "source-operation"} {
		t.Run(flag, func(t *testing.T) {
			cmd, _ := metricsTestCommand(t, "request")
			writes := 0
			server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
				data := metricsRequestTestData()
				if strings.HasPrefix(request.Query, "mutation") {
					writes++
					w.WriteHeader(503)
					return
				}
				writePipelineTestResponse(t, w, data)
			})
			defer server.Close()
			c := nvkeTestClient(server)
			if err := runNVKECredentialRequest(cmd, context.Background(), c, false); err == nil {
				t.Fatal("lost response expected")
			}
			value := "99"
			if flag == "source-operation" {
				value = nvkeTestOperation
			}
			_ = cmd.Flags().Set(flag, value)
			if err := runNVKECredentialRequest(cmd, context.Background(), c, false); err == nil || writes != 1 {
				t.Fatalf("pending tuple changed: %v writes=%d", err, writes)
			}
		})
	}
}

func TestNVKECredentialViewsPreserveUnknownsPrecisionAndServerPaging(t *testing.T) {
	for _, kind := range []string{"observation", "samples", "certificates"} {
		t.Run(kind, func(t *testing.T) {
			cmd, out := metricsTestCommand(t, kind)
			if kind != "observation" {
				_ = cmd.Flags().Set("search", "cp-1")
				_ = cmd.Flags().Set("after", "opaque-cursor")
				_ = cmd.Flags().Set("limit", "2")
			}
			server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
				data := nvkeBaseData()
				field := map[string]string{"observation": "astroliftNvkeCredentialObservation", "samples": "astroliftNvkeCredentialMetricsPage", "certificates": "astroliftNvkeCredentialCertificatesPage"}[kind]
				if strings.Contains(request.Query, field+"(") {
					if request.Variables["site"] != nvkeTestSite || request.Variables["op"] != nvkeTestOperation || request.Variables["actor"] != float64(42) || request.Variables["version"] != float64(9) {
						t.Error("historical review changed")
					}
					row := map[string]any{"siteId": nvkeTestSite, "operationId": nvkeTestOperation, "operationVersion": 9, "scrapedAt": "2026-11-08T00:00:00Z"}
					if kind == "observation" {
						row["sourceSiteVersion"], row["clusterId"], row["sourceOperationId"], row["sourceVersion"], row["authorityId"], row["authorityVersion"], row["clusterReported"], row["collectionSucceeded"] = 7, nvkeTestSite, nvkeNativeTestSource, 3, nvkeTestOrg, 3, false, false
					} else {
						if request.Variables["after"] != "opaque-cursor" || request.Variables["search"] != "cp-1" || request.Variables["limit"] != float64(2) {
							t.Error("server paging changed")
						}
						row["totalCount"], row["nextCursor"] = 3, "next"
						if kind == "samples" {
							row["items"] = []map[string]any{{"key": "key", "metric": "serving_csr_count", "value": "9007199254740993"}}
						} else {
							row["items"] = []map[string]any{{"key": "key", "node": "cp-1", "credential": "super-admin", "required": false, "latestInspectionAvailable": false, "observationAvailable": true, "reportedNotAfter": "2026-11-07T00:00:00Z", "reportedExpiredAtScrape": true}}
						}
					}
					data[field] = map[string]any{"ok": true, "data": row}
				}
				writePipelineTestResponse(t, w, data)
			})
			defer server.Close()
			if err := runNVKECredentialView(cmd, context.Background(), nvkeTestClient(server), nvkeTestOperation, kind); err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if json.Unmarshal(out.Bytes(), &value) != nil {
				t.Fatal(out.String())
			}
			if kind == "observation" && (value["clusterReported"] != false || value["collectionSucceeded"] != false || value["monitoringEnabled"] != nil || value["renewalAutomationEnabled"] != nil) {
				t.Fatal("missing evidence became health")
			}
			if kind == "samples" && !strings.Contains(out.String(), `"9007199254740993"`) {
				t.Fatal("reported precision lost")
			}
		})
	}
}

func TestNVKEMetricsCatalogAndUnconfirmedReplies(t *testing.T) {
	for _, mode := range []string{"catalog", "catalog-missing", "catalog-revision", "verify-marker", "verify-version", "configure-target", "configure-credentials", "observation-version", "observation-time", "observation-missing-cluster", "samples-missing-items"} {
		t.Run(mode, func(t *testing.T) {
			verb := "catalog"
			if strings.HasPrefix(mode, "verify-") {
				verb = "verify"
			}
			if strings.HasPrefix(mode, "configure-") {
				verb = "configure"
			}
			if strings.HasPrefix(mode, "observation-") {
				verb = "observation"
			}
			if strings.HasPrefix(mode, "samples-") {
				verb = "samples"
			}
			cmd, out := metricsTestCommand(t, verb)
			if mode == "configure-credentials" {
				_ = cmd.Flags().Set("clear-credentials", "true")
			}
			server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
				data := nvkeBaseData()
				switch verb {
				case "catalog":
					catalog := map[string]any{"revision": nvkecatalog.Pinned().Revision, "credentialMetrics": map[string]any{"metrics": []string{"pinned"}}}
					if mode == "catalog-missing" {
						delete(catalog, "credentialMetrics")
					}
					if mode == "catalog-revision" {
						catalog["revision"] = strings.Repeat("0", 40)
					}
					data["astroliftNvkeContract"] = catalog
				case "verify", "configure":
					row := metricsTestMetadata(verb)
					if mode == "verify-marker" {
						row["verificationCode"] = "HEALTHY"
					}
					if mode == "verify-version" {
						row["siteVersion"] = 999
					}
					if mode == "configure-target" {
						row["endpoint"] = "https://replacement.test/metrics"
					}
					field := "verifyNvkeMetricsAuthority"
					if verb == "configure" {
						field = "configureNvkeMetricsAuthority"
					}
					data[field] = map[string]any{"ok": true, "data": row}
				case "observation", "samples":
					row := map[string]any{"siteId": nvkeTestSite, "operationId": nvkeTestOperation, "operationVersion": 9, "scrapedAt": "2026-11-08T00:00:00Z", "sourceSiteVersion": 7, "clusterId": nvkeTestSite, "sourceOperationId": nvkeNativeTestSource, "sourceVersion": 3, "authorityId": nvkeTestOrg, "authorityVersion": 3, "clusterReported": false}
					if mode == "observation-version" {
						row["operationVersion"] = 10
					}
					if mode == "observation-time" {
						delete(row, "scrapedAt")
					}
					if mode == "observation-missing-cluster" {
						delete(row, "clusterReported")
					}
					field := "astroliftNvkeCredentialObservation"
					if verb == "samples" {
						field = "astroliftNvkeCredentialMetricsPage"
					}
					data[field] = map[string]any{"ok": true, "data": row}
				}
				writePipelineTestResponse(t, w, data)
			})
			defer server.Close()
			var err error
			switch verb {
			case "catalog":
				err = runNVKEMetricsCatalog(cmd, context.Background(), nvkeTestClient(server), nil)
			case "verify":
				err = runNVKEMetricsReview(cmd, context.Background(), nvkeTestClient(server), verb)
			case "configure":
				err = runNVKEMetricsConfigure(cmd, context.Background(), nvkeTestClient(server), nil)
			default:
				err = runNVKECredentialView(cmd, context.Background(), nvkeTestClient(server), nvkeTestOperation, verb)
			}
			if mode == "catalog" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || out.Len() != 0 {
				t.Fatalf("unconfirmed reply was printed: err=%v", err)
			}
		})
	}
}

func TestNVKECredentialRecoveryRejectsScopeAndMalformedMetadata(t *testing.T) {
	for _, field := range []string{"server", "organizationId", "actorUserId", "kind", "ref", "sourceOperationId", "authorityVersion", "format"} {
		t.Run(field, func(t *testing.T) {
			cmd, _ := metricsTestCommand(t, "request")
			file := resourceStringFlag(cmd, "request-file")
			writes := 0
			server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
				if strings.HasPrefix(request.Query, "mutation") {
					writes++
					w.WriteHeader(503)
					return
				}
				writePipelineTestResponse(t, w, metricsRequestTestData())
			})
			defer server.Close()
			c := nvkeTestClient(server)
			if err := runNVKECredentialRequest(cmd, context.Background(), c, false); err == nil {
				t.Fatal("missing response expected")
			}
			data, err := privatefile.ReadFile(file, 16384)
			if err != nil {
				t.Fatal(err)
			}
			var row map[string]any
			if json.Unmarshal(data, &row) != nil {
				t.Fatal("invalid fixture")
			}
			row[field] = "changed"
			if field == "actorUserId" {
				row[field] = 99
			}
			if field == "authorityVersion" || field == "format" {
				row[field] = 0
			}
			data, err = json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err = runNVKECredentialRequest(cmd, context.Background(), c, false); err == nil || writes != 1 {
				t.Fatalf("changed recovery admitted: %v writes=%d", err, writes)
			}
		})
	}
}
