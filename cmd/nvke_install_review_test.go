package cmd

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/nvkecatalog"
)

func installReviewData() map[string]any {
	return map[string]any{
		"siteId": nvkeTestSite, "siteVersion": 7, "operationId": nvkeTestOperation, "operationVersion": 4,
		"authorityId": nvkeTestOperation, "authorityVersion": 3, "instanceId": nvkeNativeTestSource,
		"bundleManifestSha256": strings.Repeat("a", 64), "nativeRevision": nvkecatalog.Pinned().Revision,
		"nativeRunId": nvkeTestOrg, "reviewDigest": strings.Repeat("b", 64),
		"observedAt": "2026-11-08T21:00:00Z", "originalStartedAt": "2026-11-08T20:22:39Z",
		"nativeArgumentsSha256": strings.Repeat("c", 64), "nativeExitCode": 1,
		"apiReady": true, "continuationAuthorized": false, "reviewerSha256": strings.Repeat("d", 64),
		"qualificationHelperSha256": strings.Repeat("e", 64),
		"evidence":                  map[string]any{"guardSha256": strings.Repeat("f", 64), "startedSha256": strings.Repeat("a", 64), "finishedSha256": strings.Repeat("b", 64), "stdoutSha256": strings.Repeat("c", 64), "stderrSha256": strings.Repeat("d", 64)},
		"workloads":                 []map[string]any{{"namespace": "nktp", "kind": "StatefulSet", "name": "collector", "generation": "9007199254740993", "observedGeneration": nil, "desiredReplicas": "2", "readyReplicas": nil, "updatedReplicas": "1", "observation": "requires-review"}},
		"privateNativeInput":        "PRIVATE_MUST_NOT_BE_PRINTED",
	}
}

func TestNVKEInstallReviewBindsOriginalAuthorityAndPreservesUnknownPrecision(t *testing.T) {
	for _, kind := range []string{"valid", "settled-source", "wrong-command", "stale-source", "stale-authority", "site", "authority", "instance", "run", "revision", "digest", "missing-continuation", "continuation", "missing-ready", "api-unready", "successful-native", "missing-evidence", "invalid-decimal", "overflow", "duplicate", "false-ready", "refused"} {
		t.Run(kind, func(t *testing.T) {
			cmd, out := agentTestCommand(t, "install-review")
			_ = cmd.Flags().Set("version", "4")
			writes := 0
			server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
				data := agentTestData()
				source := map[string]any{"id": nvkeTestOperation, "version": 4, "siteId": nvkeTestSite, "requestId": nvkeNativeTestSource, "operationKey": "tooling.management.install", "status": "UNCERTAIN"}
				switch kind {
				case "settled-source":
					source["status"] = "SUCCEEDED"
				case "wrong-command":
					source["operationKey"] = "tooling.generate.sitespec"
				case "stale-source":
					source["version"] = 5
				case "stale-authority":
					data["astroliftNvkeOperatorAuthority"].(map[string]any)["version"] = 4
				}
				data["astroliftNvkeOperation"] = source
				if strings.HasPrefix(request.Query, "mutation") {
					writes++
					if !strings.Contains(request.Query, "reviewNvkeOperatorInstall(") || strings.Contains(request.Query, "requestNvkeOperatorJob") || strings.Contains(request.Query, "stopNvkeOperatorJob") {
						t.Error("review submitted an infrastructure action")
					}
					input := request.Variables["input"].(map[string]any)
					if len(input) != 5 || input["operationId"] != nvkeTestOperation || input["expectedActorId"] != float64(42) || input["expectedSiteVersion"] != float64(7) || input["expectedAuthorityVersion"] != float64(3) || input["expectedOperationVersion"] != float64(4) {
						t.Error("review lost original identity or reviewed versions")
					}
					row := installReviewData()
					workloads := row["workloads"].([]map[string]any)
					switch kind {
					case "site":
						row["siteId"] = nvkeNativeTestSource
					case "authority":
						row["authorityId"] = nvkeNativeTestSource
					case "instance":
						row["instanceId"] = nvkeTestOrg
					case "run":
						row["nativeRunId"] = "PRIVATE_BAD_RUN"
					case "revision":
						row["nativeRevision"] = strings.Repeat("b", 40)
					case "digest":
						row["reviewDigest"] = "PRIVATE_BAD_DIGEST"
					case "missing-continuation":
						delete(row, "continuationAuthorized")
					case "continuation":
						row["continuationAuthorized"] = true
					case "missing-ready":
						delete(row, "apiReady")
					case "api-unready":
						row["apiReady"] = false
					case "successful-native":
						row["nativeExitCode"] = 0
					case "missing-evidence":
						delete(row["evidence"].(map[string]any), "guardSha256")
					case "invalid-decimal":
						workloads[0]["generation"] = "01"
					case "overflow":
						workloads[0]["generation"] = "9223372036854775808"
					case "duplicate":
						row["workloads"] = append(workloads, workloads[0])
					case "false-ready":
						workloads[0]["observation"] = "ready"
					}
					data["reviewNvkeOperatorInstall"] = map[string]any{"ok": true, "data": row}
					if kind == "refused" {
						data["reviewNvkeOperatorInstall"] = map[string]any{"ok": false, "errors": []map[string]any{{"code": "VERSION_MISMATCH", "message": "PRIVATE_REFUSAL"}}}
					}
				}
				writePipelineTestResponse(t, w, data)
			})
			defer server.Close()
			err := runNVKEAgentInstallReview(cmd, context.Background(), nvkeTestClient(server), []string{nvkeTestOperation})
			if (err == nil) != (kind == "valid") || strings.Contains(out.String(), "PRIVATE") || (err != nil && strings.Contains(err.Error(), "PRIVATE")) {
				t.Fatalf("unsafe review kind=%s writes=%d err=%v", kind, writes, err)
			}
			wantWrites := 1
			if kind == "settled-source" || kind == "wrong-command" || kind == "stale-source" || kind == "stale-authority" {
				wantWrites = 0
			}
			if writes != wantWrites {
				t.Fatalf("review actions=%d expected=%d", writes, wantWrites)
			}
			if kind == "valid" && (!strings.Contains(out.String(), `"9007199254740993"`) || !strings.Contains(out.String(), `"readyReplicas": null`) || !strings.Contains(out.String(), `"continuationAuthorized": false`)) {
				t.Fatalf("precision, unknown or non-authorization was lost: %s", out.String())
			}
		})
	}
}
