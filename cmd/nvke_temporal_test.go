package cmd

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

func nvkeTemporalTestCommand(t *testing.T, verb string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd, _, err := newNVKETemporalCommand().Find([]string{verb})
	if err != nil {
		t.Fatal(err)
	}
	cmd.SetContext(context.Background())
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	values := map[string]string{"site": nvkeTestSite}
	if verb != "get" {
		values["site-version"] = "7"
		values["authority-version"] = "3"
	}
	if verb == "configure" {
		values["address"] = "native.test:7233"
		values["namespace"] = "native-only"
		values["task-queue"] = "native-queue"
	}
	for flag, value := range values {
		if err = cmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	return cmd, out
}

func temporalTestMetadata(verb string) map[string]any {
	row := map[string]any{"id": nvkeTestOperation, "version": 4, "siteVersion": 8, "siteId": nvkeTestSite, "address": "native.test:7233", "namespace": "native-only", "taskQueue": "native-queue", "useTls": true, "timeoutSeconds": 30, "isEnabled": verb == "configure" || verb == "enable" || verb == "verify", "hasCredentials": true}
	if verb == "verify" {
		row["version"] = 3
		row["siteVersion"] = 7
		row["verificationCode"] = "NAMESPACE_VERIFIED"
		row["verifiedAt"] = "2026-11-07T00:00:00Z"
	}
	if verb == "remove" {
		row["deletedAt"] = "2026-11-07T00:00:00Z"
	}
	return row
}

func TestNVKETemporalReviewedWritesAndMetadataOnly(t *testing.T) {
	for _, verb := range []string{"configure", "verify", "enable", "disable", "remove"} {
		t.Run(verb, func(t *testing.T) {
			cmd, out := nvkeTemporalTestCommand(t, verb)
			if verb == "configure" {
				_ = cmd.Flags().Set("credentials-file", "-")
				cmd.SetIn(strings.NewReader(`{"apiKey":"PRIVATE_NATIVE_KEY","caPem":"PRIVATE_NATIVE_CA"}`))
			}
			mutations := 0
			server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
				data := nvkeBaseData()
				if strings.HasPrefix(request.Query, "mutation") {
					mutations++
					input := request.Variables["input"].(map[string]interface{})
					if input["siteId"] != nvkeTestSite || input["expectedSiteVersion"] != float64(7) || input["expectedVersion"] != float64(3) || input["expectedActorId"] != float64(42) {
						t.Error("reviewed authority changed")
					}
					field := map[string]string{"configure": "configureNvkeTemporalAuthority", "verify": "verifyNvkeTemporalAuthority", "enable": "setNvkeTemporalAuthorityEnabled", "disable": "setNvkeTemporalAuthorityEnabled", "remove": "removeNvkeTemporalAuthority"}[verb]
					if verb == "configure" {
						credentials := input["credentials"].(map[string]interface{})
						if credentials["apiKey"] != "PRIVATE_NATIVE_KEY" || input["namespace"] != "native-only" || input["taskQueue"] != "native-queue" || input["useTls"] != true {
							t.Error("native credential/target tuple changed")
						}
					}
					if verb == "enable" || verb == "disable" {
						if input["enabled"] != (verb == "enable") {
							t.Error("enabled state changed")
						}
					}
					row := temporalTestMetadata(verb)
					row["apiKey"] = "PRIVATE_BAD_SERVER_ECHO"
					data[field] = map[string]any{"ok": true, "data": row}
				}
				writePipelineTestResponse(t, w, data)
			})
			defer server.Close()
			var err error
			if verb == "configure" {
				err = runNVKETemporalConfigure(cmd, context.Background(), nvkeTestClient(server), nil)
			} else {
				err = runNVKETemporalReview(cmd, context.Background(), nvkeTestClient(server), verb)
			}
			if err != nil || mutations != 1 || strings.Contains(out.String(), "PRIVATE") {
				t.Fatalf("unsafe reviewed write: %v", err)
			}
		})
	}
}

func TestNVKETemporalCredentialPreserveClearAndRefusal(t *testing.T) {
	for _, mode := range []string{"preserve", "clear", "null", "unknown", "unpaired", "plaintext", "trailing"} {
		t.Run(mode, func(t *testing.T) {
			cmd, out := nvkeTemporalTestCommand(t, "configure")
			if mode == "clear" {
				_ = cmd.Flags().Set("clear-credentials", "true")
			}
			doc := map[string]string{"null": `{"apiKey":null}`, "unknown": `{"token":"PRIVATE_HTTP_TOKEN"}`, "unpaired": `{"clientKeyPem":"PRIVATE_KEY"}`, "plaintext": `{"apiKey":"PRIVATE_KEY"}`, "trailing": `{} {"apiKey":"PRIVATE_KEY"}`}[mode]
			if doc != "" {
				_ = cmd.Flags().Set("credentials-file", "-")
				cmd.SetIn(strings.NewReader(doc))
			}
			if mode == "plaintext" {
				_ = cmd.Flags().Set("plaintext", "true")
			}
			calls, mutations := 0, 0
			server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
				calls++
				data := nvkeBaseData()
				if strings.HasPrefix(request.Query, "mutation") {
					mutations++
					input := request.Variables["input"].(map[string]interface{})
					credentials, present := input["credentials"]
					if (mode == "preserve" && present) || (mode == "clear" && (!present || len(credentials.(map[string]interface{})) != 0)) {
						t.Error("credential preservation/clearing changed")
					}
					row := temporalTestMetadata("configure")
					if mode == "clear" {
						row["hasCredentials"] = false
					}
					data["configureNvkeTemporalAuthority"] = map[string]any{"ok": true, "data": row}
				}
				writePipelineTestResponse(t, w, data)
			})
			defer server.Close()
			err := runNVKETemporalConfigure(cmd, context.Background(), nvkeTestClient(server), nil)
			if mode == "preserve" || mode == "clear" {
				if err != nil || mutations != 1 {
					t.Fatal(err)
				}
			} else {
				if err == nil || calls != 0 {
					t.Fatal("invalid credentials reached network")
				}
			}
			if strings.Contains(out.String(), "PRIVATE") || (err != nil && strings.Contains(err.Error(), "PRIVATE")) {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestNVKETemporalUncertainWriteNeverRetries(t *testing.T) {
	cmd, out := nvkeTemporalTestCommand(t, "configure")
	mutations := 0
	server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
		if strings.HasPrefix(request.Query, "mutation") {
			mutations++
			w.WriteHeader(503)
			_, _ = w.Write([]byte("PRIVATE_SERVER_FAILURE"))
			return
		}
		writePipelineTestResponse(t, w, nvkeBaseData())
	})
	defer server.Close()
	err := runNVKETemporalConfigure(cmd, context.Background(), nvkeTestClient(server), nil)
	if err == nil || mutations != 1 || !strings.Contains(err.Error(), "temporal get") || strings.Contains(err.Error()+out.String(), "PRIVATE") {
		t.Fatalf("unsafe uncertain write: %v", err)
	}
}

func TestNVKETemporalGetDistinguishesNoAuthorityFromIncompleteReply(t *testing.T) {
	for _, mode := range []string{"none", "valid", "missing", "foreign", "removed"} {
		t.Run(mode, func(t *testing.T) {
			cmd, out := nvkeTemporalTestCommand(t, "get")
			server := nvkeTestServer(t, func(_ gqlRequest, w http.ResponseWriter) {
				data := map[string]any{}
				if mode == "none" {
					data["astroliftNvkeTemporalAuthority"] = nil
				} else if mode != "missing" {
					row := temporalTestMetadata("get")
					if mode == "foreign" {
						row["siteId"] = nvkeTestOrg
					}
					if mode == "removed" {
						row["deletedAt"] = "2026-11-07T00:00:00Z"
					}
					data["astroliftNvkeTemporalAuthority"] = row
				}
				writePipelineTestResponse(t, w, data)
			})
			defer server.Close()
			err := runNVKETemporalGet(cmd, context.Background(), nvkeTestClient(server), nil)
			if (mode == "none" || mode == "valid") != (err == nil) {
				t.Fatalf("wrong metadata result: %v", err)
			}
			if mode == "none" && strings.TrimSpace(out.String()) != "null" {
				t.Fatal("missing authority should remain null")
			}
		})
	}
}

func TestNVKETemporalReplyFencing(t *testing.T) {
	for _, verb := range []string{"configure", "verify", "enable", "disable", "remove"} {
		for _, change := range []string{"site", "version", "site-version", "enabled", "verification", "deleted"} {
			t.Run(verb+"/"+change, func(t *testing.T) {
				row := &api.NVKETemporalAuthority{ID: nvkeTestOperation, SiteID: nvkeTestSite, Version: 4, SiteVersion: 8, TimeoutSeconds: 30, IsEnabled: verb == "configure" || verb == "enable" || verb == "verify"}
				stamp := "2026-11-07T00:00:00Z"
				if verb == "verify" {
					row.Version = 3
					row.SiteVersion = 7
					row.VerifiedAt = &stamp
					row.VerificationCode = "NAMESPACE_VERIFIED"
				}
				if verb == "remove" {
					row.DeletedAt = &stamp
				}
				switch change {
				case "site":
					row.SiteID = nvkeTestOrg
				case "version":
					row.Version = 0
				case "site-version":
					row.SiteVersion = 0
				case "enabled":
					row.IsEnabled = !row.IsEnabled
				case "verification":
					if verb == "verify" {
						row.VerifiedAt = nil
					} else {
						row.VerifiedAt = &stamp
					}
				case "deleted":
					if row.DeletedAt == nil {
						row.DeletedAt = &stamp
					} else {
						row.DeletedAt = nil
					}
				}
				if validateNVKETemporalReply(row, api.NVKETemporalReview{SiteID: nvkeTestSite, ExpectedVersion: 3, ExpectedSiteVersion: 7}, verb) == nil {
					t.Fatal("changed reply accepted")
				}
			})
		}
	}
}
