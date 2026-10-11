package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func TestReviewedClusterIDFreshInstallAndLostReplyNeverDiscoverInventory(t *testing.T) {
	for _, kind := range []string{"agent", "collector"} {
		t.Run(kind, func(t *testing.T) {
			c, out, file := collectorCommand(t)
			c.Flags().String("cluster-id", reviewedPipelineTestID, "")
			var inputs []map[string]interface{}
			reviews := 0
			srv := agentInstallServer(t, func(q gqlRequest, w http.ResponseWriter) {
				if q.Organization != reviewedPipelineTestOrg {
					t.Error("reviewed call changed organization")
				}
				if strings.Contains(q.Query, "astroliftClusters") {
					t.Error("direct GUID attempted operator inventory")
					w.WriteHeader(http.StatusForbidden)
					return
				}
				data := collectorHTTPData()
				if strings.Contains(q.Query, "Review(") {
					reviews++
					if q.Variables["clusterId"] != reviewedPipelineTestID {
						t.Error("review selected another GUID")
					}
				}
				if strings.Contains(q.Query, "mutation InstallCluster") {
					input := q.Variables["input"].(map[string]interface{})
					inputs = append(inputs, input)
					if _, err := os.Stat(file); err != nil {
						t.Error("request was not durable before dispatch")
					}
					if len(inputs) == 1 {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					if kind == "agent" {
						data["installClusterAgent"] = map[string]interface{}{"ok": true, "data": agentInstallResult(input["requestId"].(string), "QUEUED")}
					} else {
						data["astroliftInstallClusterLogCollector"] = map[string]interface{}{"ok": true, "data": collectorResult(input, "QUEUED")}
					}
				}
				writePipelineTestResponse(t, w, data)
			})
			defer srv.Close()
			client := api.NewClient(srv.URL, "PRIVATE_SCOPED_BEARER", false)
			client.SetOrg(reviewedPipelineTestOrg)
			run := func() error {
				if kind == "agent" {
					return runClusterInstallAgent(c, context.Background(), client, "", "", 11)
				}
				return runClusterInstallLogCollector(c, context.Background(), client, "", 30)
			}
			if err := run(); err == nil || !strings.Contains(err.Error(), "unknown") {
				t.Fatalf("lost response did not preserve uncertainty: %v", err)
			}
			before, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if err := run(); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(file)
			if reviews != 1 || len(inputs) != 2 || !reflect.DeepEqual(inputs[0], inputs[1]) || !bytes.Equal(before, after) {
				t.Fatal("direct GUID recovery replaced or refreshed the original tuple")
			}
			if inputs[0]["clusterId"] != reviewedPipelineTestID || inputs[0]["expectedVersion"] != float64(7) || strings.Contains(string(after)+out.String(), "PRIVATE_SCOPED_BEARER") {
				t.Fatal("incorrect target/version or credential material persisted")
			}
		})
	}
}

func TestReviewedClusterIDRecoversSlugTupleAndRefusesChangedScope(t *testing.T) {
	for _, kind := range []string{"agent", "collector"} {
		for _, change := range []string{"none", "cluster", "organization", "actor"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				c, _, file := collectorCommand(t)
				id := reviewedPipelineTestID
				if change == "cluster" {
					id = uuid.NewString()
				}
				c.Flags().String("cluster-id", id, "")
				mutations := 0
				requestID := uuid.NewString()
				srv := agentInstallServer(t, func(q gqlRequest, w http.ResponseWriter) {
					if strings.Contains(q.Query, "Review(") || strings.Contains(q.Query, "astroliftClusters") {
						t.Error("replay refreshed original review or discovered inventory")
					}
					data := collectorHTTPData()
					if change == "actor" {
						data["astroliftMyProfile"] = map[string]interface{}{"userId": 43}
					}
					if strings.Contains(q.Query, "mutation InstallCluster") {
						mutations++
						input := q.Variables["input"].(map[string]interface{})
						if input["requestId"] != requestID || input["clusterId"] != reviewedPipelineTestID || input["expectedVersion"] != float64(7) {
							t.Error("original request identity changed")
						}
						if kind == "agent" {
							if input["expectedSource"] != agentInstallTestSource {
								t.Error("original agent source changed")
							}
							data["installClusterAgent"] = map[string]interface{}{"ok": true, "data": agentInstallResult(requestID, "QUEUED")}
						} else {
							if input["expectedSource"] != collectorTestSource || input["retentionDays"] != float64(90) {
								t.Error("original collector source or retention changed")
							}
							data["astroliftInstallClusterLogCollector"] = map[string]interface{}{"ok": true, "data": collectorResult(input, "QUEUED")}
						}
					}
					writePipelineTestResponse(t, w, data)
				})
				defer srv.Close()
				r := reviewedStartRequest{Format: 1, Kind: clusterAgentInstallKind, Server: srv.URL, OrganizationID: reviewedPipelineTestOrg, ActorUserID: 42, TargetID: reviewedPipelineTestID, TargetName: "production", RequestID: requestID, Version: 7, ExpectedSource: agentInstallTestSource}
				if kind == "agent" {
					if err := createReviewedRequest(file, r); err != nil {
						t.Fatal(err)
					}
				} else {
					r.Kind, r.ExpectedSource = collectorRequestKind, collectorTestSource
					if err := createCollectorInstallRequest(file, collectorInstallRequest{reviewedStartRequest: r, RetentionDays: 90}); err != nil {
						t.Fatal(err)
					}
				}
				before, _ := os.ReadFile(file)
				client := api.NewClient(srv.URL, "fixture", false)
				client.SetOrg(reviewedPipelineTestOrg)
				if change == "organization" {
					client.SetOrg(uuid.NewString())
				}
				var err error
				if kind == "agent" {
					err = runClusterInstallAgent(c, context.Background(), client, "", "", 0)
				} else {
					err = runClusterInstallLogCollector(c, context.Background(), client, "", 30)
				}
				if change == "none" && (err != nil || mutations != 1) || change != "none" && (err == nil || mutations != 0) {
					t.Fatalf("wrong replay outcome: mutations=%d error=%v", mutations, err)
				}
				after, _ := os.ReadFile(file)
				if !bytes.Equal(before, after) {
					t.Fatal("replay changed the original slug-based file")
				}
			})
		}
	}
}

func TestReviewedClusterSelectorUsageRefusesBeforeHTTP(t *testing.T) {
	for _, testcase := range []struct{ slug, id string }{{}, {"", "invalid"}, {"", uuid.Nil.String()}, {"production", reviewedPipelineTestID}} {
		c, _, _ := collectorCommand(t)
		c.Flags().String("cluster-id", testcase.id, "")
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
		client := api.NewClient(srv.URL, "fixture", false)
		if err := runClusterInstallAgent(c, context.Background(), client, testcase.slug, "", 0); err == nil {
			t.Error("invalid selector admitted for agent")
		}
		if err := runClusterInstallLogCollector(c, context.Background(), client, testcase.slug, 30); err == nil {
			t.Error("invalid selector admitted for collector")
		}
		if err := runClusterCollectorReview(c, context.Background(), client, testcase.slug, 30); err == nil {
			t.Error("invalid selector admitted for review")
		}
		srv.Close()
		if calls != 0 {
			t.Fatal("invalid selector made an HTTP call")
		}
	}
}

func TestDirectCollectorReviewAndPreciseSlugInventoryRefusal(t *testing.T) {
	reviews, inventory := 0, 0
	srv := agentInstallServer(t, func(q gqlRequest, w http.ResponseWriter) {
		if strings.Contains(q.Query, "astroliftClusters") {
			inventory++
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if strings.Contains(q.Query, "Review(") {
			reviews++
		}
		writePipelineTestResponse(t, w, collectorHTTPData())
	})
	defer srv.Close()
	client := api.NewClient(srv.URL, "fixture", false)
	client.SetOrg(reviewedPipelineTestOrg)
	c, out, _ := collectorCommand(t)
	c.Flags().String("cluster-id", reviewedPipelineTestID, "")
	_ = c.Flags().Set("json", "true")
	if err := runClusterCollectorReview(c, context.Background(), client, "", 30); err != nil || !strings.Contains(out.String(), reviewedPipelineTestID) {
		t.Fatalf("direct review unavailable: %v", err)
	}
	c, _, _ = collectorCommand(t)
	if err := runClusterCollectorReview(c, context.Background(), client, "production", 30); err == nil || !strings.Contains(err.Error(), "cluster.register") || !strings.Contains(err.Error(), "--cluster-id") {
		t.Fatalf("imprecise discovery refusal: %v", err)
	}
	if reviews != 1 || inventory != 1 {
		t.Fatal("GUID lookup discovered inventory or slug denial fell back")
	}
}

func TestReviewedStatusClusterGUIDMustMatchBeforeOutput(t *testing.T) {
	for _, kind := range []string{"agent", "collector"} {
		for _, mismatch := range []bool{false, true} {
			t.Run(kind, func(t *testing.T) {
				c, out, _ := collectorCommand(t)
				c.Flags().String("cluster-id", reviewedPipelineTestID, "")
				_ = c.Flags().Set("json", "true")
				srv := agentInstallServer(t, func(q gqlRequest, w http.ResponseWriter) {
					if strings.Contains(q.Query, "astroliftClusters") {
						t.Error("status enumerated inventory")
					}
					requestID := uuid.NewString()
					data := map[string]interface{}{}
					if kind == "agent" {
						row := agentInstallResult(requestID, "SUCCEEDED")
						if mismatch {
							row["clusterId"] = uuid.NewString()
						}
						data["astroliftClusterAgentInstall"] = row
					} else {
						row := collectorResult(map[string]interface{}{"clusterId": reviewedPipelineTestID, "requestId": requestID, "expectedVersion": 7, "expectedSource": collectorTestSource, "retentionDays": 30}, "ACTIVATED")
						row["retryable"] = false
						row["postLossVerifiedAt"], row["activatedAt"], row["activatedClusterVersion"] = "2026-10-03T01:10:00Z", "2026-10-03T01:11:00Z", 8
						if mismatch {
							row["clusterId"] = uuid.NewString()
						}
						data["astroliftClusterLogCollectorOperation"] = row
					}
					writePipelineTestResponse(t, w, data)
				})
				defer srv.Close()
				client := api.NewClient(srv.URL, "fixture", false)
				client.SetOrg(reviewedPipelineTestOrg)
				var err error
				if kind == "agent" {
					err = runClusterAgentInstallStatus(c, context.Background(), client, agentInstallTestID)
				} else {
					err = runClusterLogCollectorStatus(c, context.Background(), client, agentInstallTestID)
				}
				if mismatch && (err == nil || !strings.Contains(err.Error(), "another cluster") || out.Len() != 0) || !mismatch && (err != nil || out.Len() == 0) {
					t.Fatalf("incorrect selected status outcome: error=%v output=%s", err, out.String())
				}
			})
		}
	}
}

func TestReviewedNativeSelectorValidationPrecedesCredentialLoading(t *testing.T) {
	for _, original := range []*cobra.Command{clusterInstallAgentCmd, clusterLogCollectorReviewCmd, clusterInstallLogCollectorCmd, clusterAgentInstallStatusCmd, clusterLogCollectorStatusCmd} {
		t.Run(original.Name(), func(t *testing.T) {
			// Use the production Args function with an executing Cobra command. A
			// refused selector must never enter RunE (which loads credentials).
			enteredRun := false
			c := &cobra.Command{Use: original.Name(), Args: original.Args, RunE: func(*cobra.Command, []string) error { enteredRun = true; return nil }}
			c.SetOut(&bytes.Buffer{})
			c.SetErr(&bytes.Buffer{})
			c.Flags().String("cluster-id", "", "")
			if original.Flags().Lookup("slug") != nil {
				c.Flags().String("slug", "", "")
			}
			c.SetArgs([]string{"--cluster-id", "invalid"})
			if err := c.Execute(); err == nil || !strings.Contains(err.Error(), "canonical nonzero") || enteredRun {
				t.Fatalf("invalid GUID reached credential loading: %v", err)
			}
		})
	}
}

func TestDirectClusterGUIDCannotBypassDeniedOrMismatchedReview(t *testing.T) {
	for _, kind := range []string{"agent", "collector"} {
		for _, outcome := range []string{"denied", "another-cluster"} {
			t.Run(kind+"/"+outcome, func(t *testing.T) {
				c, out, file := collectorCommand(t)
				c.Flags().String("cluster-id", reviewedPipelineTestID, "")
				reviews := 0
				srv := agentInstallServer(t, func(q gqlRequest, w http.ResponseWriter) {
					if strings.Contains(q.Query, "astroliftClusters") || strings.Contains(q.Query, "mutation ") {
						t.Error("denied exact review caused inventory fallback or installation")
					}
					data := collectorHTTPData()
					if strings.Contains(q.Query, "Review(") {
						reviews++
						if outcome == "denied" {
							w.WriteHeader(http.StatusForbidden)
							return
						}
						field := "astroliftClusterAgentInstallReview"
						if kind == "collector" {
							field = "astroliftClusterLogCollectorReview"
						}
						data[field].(map[string]interface{})["clusterId"] = uuid.NewString()
					}
					writePipelineTestResponse(t, w, data)
				})
				defer srv.Close()
				client := api.NewClient(srv.URL, "fixture", false)
				client.SetOrg(reviewedPipelineTestOrg)
				var err error
				if kind == "agent" {
					err = runClusterInstallAgent(c, context.Background(), client, "", "", 0)
				} else {
					err = runClusterInstallLogCollector(c, context.Background(), client, "", 30)
				}
				if err == nil || reviews != 1 || out.Len() != 0 {
					t.Fatalf("invalid exact review admitted: %v", err)
				}
				if _, err := os.Stat(file); !os.IsNotExist(err) {
					t.Fatal("refused review created an installation request")
				}
			})
		}
	}
}
