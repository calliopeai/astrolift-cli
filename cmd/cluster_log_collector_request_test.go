package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/google/uuid"
)

func collectorRequestFixture() collectorInstallRequest {
	return collectorInstallRequest{reviewedStartRequest: reviewedStartRequest{
		Format: 1, Kind: collectorRequestKind, Server: "https://platform.invalid",
		OrganizationID: reviewedPipelineTestOrg, ActorUserID: 42,
		TargetID: reviewedPipelineTestID, TargetName: "production",
		RequestID: uuid.NewString(), Version: 7, ExpectedSource: "original-reviewed-source",
	}, RetentionDays: 30}
}

func TestCollectorRecoveryUsesCurrentHTTPActorAndOrganizationWithoutReplacingTuple(t *testing.T) {
	for _, change := range []string{"actor", "organization", "credential"} {
		t.Run(change, func(t *testing.T) {
			c, _, filename := agentInstallCommand(t)
			requests, mutations := 0, 0
			srv := agentInstallServer(t, func(q gqlRequest, w http.ResponseWriter) {
				requests++
				if strings.Contains(q.Query, "mutation") {
					mutations++
				}
				if change == "credential" {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte("PRIVATE_HTTP_RESPONSE_MARKER"))
					return
				}
				actor := 42
				if change == "actor" {
					actor = 43
				}
				writePipelineTestResponse(t, w, map[string]interface{}{
					"astroliftMyProfile": map[string]interface{}{"userId": actor, "username": "operator"},
				})
			})
			defer srv.Close()
			r := collectorRequestFixture()
			r.Server = srv.URL
			if err := createCollectorInstallRequest(filename, r); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(filename)
			client := api.NewClient(srv.URL, "PRIVATE_HTTP_BEARER_MARKER", false)
			client.SetOrg(r.OrganizationID)
			if change == "organization" {
				client.SetOrg(uuid.NewString())
			}
			server, actor, err := reviewedRequestScope(c, client)
			if err == nil {
				err = r.checkRecovery(server, client.Org(), actor, r.TargetName, r.RetentionDays, true)
			}
			if err == nil || strings.Contains(err.Error(), "PRIVATE_HTTP_RESPONSE_MARKER") ||
				strings.Contains(err.Error(), "PRIVATE_HTTP_BEARER_MARKER") {
				t.Fatalf("current credential scope did not refuse safely: %v", err)
			}
			after, _ := os.ReadFile(filename)
			if requests != 1 || mutations != 0 || !bytes.Equal(before, after) {
				t.Fatal("scope revalidation mutated or replaced the original recovery tuple")
			}
		})
	}
}

func TestCollectorRequestStoresOriginalPrivateTupleAndNeverReplaces(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "collector.json")
	original := collectorRequestFixture()
	if err := createCollectorInstallRequest(filename, original); err != nil {
		t.Fatal(err)
	}
	f, err := privatefile.Open(filename, 16384)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	recovered, err := readCollectorInstallRequest(filename)
	if err != nil || *recovered != original {
		t.Fatalf("original metadata not recovered: %v", err)
	}
	if err := createCollectorInstallRequest(filename, collectorRequestFixture()); err == nil {
		t.Fatal("original request was replaced")
	}
	again, err := readCollectorInstallRequest(filename)
	if err != nil || *again != original {
		t.Fatal("exclusive creation changed the original tuple")
	}
}

func TestCollectorRequestMissingOriginalTupleRefusesWithoutEcho(t *testing.T) {
	for _, change := range []string{"source", "retention", "version", "target", "request", "actor", "server", "kind", "name"} {
		t.Run(change, func(t *testing.T) {
			r := collectorRequestFixture()
			switch change {
			case "source":
				r.ExpectedSource = ""
			case "retention":
				r.RetentionDays = 0
			case "version":
				r.Version = 0
			case "target":
				r.TargetID = uuid.Nil.String()
			case "request":
				r.RequestID = "PRIVATE_TUPLE_MARKER"
			case "actor":
				r.ActorUserID = 0
			case "server":
				r.Server = ""
			case "kind":
				r.Kind = "workflow-definition"
			case "name":
				r.TargetName = ""
			}
			data, _ := json.Marshal(r)
			filename := filepath.Join(t.TempDir(), "collector.json")
			if err := privatefile.Write(filename, data); err != nil {
				t.Fatal(err)
			}
			if _, err := readCollectorInstallRequest(filename); err == nil || strings.Contains(err.Error(), "PRIVATE_TUPLE_MARKER") {
				t.Fatalf("unsafe missing-tuple outcome: %v", err)
			}
			after, _ := os.ReadFile(filename)
			if string(after) != string(data) {
				t.Fatal("invalid saved tuple was changed")
			}
		})
	}
}

func TestCollectorRequestRejectsUnknownBodiesExtraDataAndOversize(t *testing.T) {
	for _, body := range []string{
		`{"PRIVATE_BODY_MARKER":"value"}`,
		`{"format":1} {"PRIVATE_BODY_MARKER":"extra"}`,
		strings.Repeat("PRIVATE_BODY_MARKER", 1000),
	} {
		filename := filepath.Join(t.TempDir(), "collector.json")
		if err := privatefile.Write(filename, []byte(body)); err != nil {
			t.Fatal(err)
		}
		if _, err := readCollectorInstallRequest(filename); err == nil || strings.Contains(err.Error(), "PRIVATE_BODY_MARKER") {
			t.Fatalf("request body escaped into diagnostic: %v", err)
		}
	}
}

func TestCollectorRecoveryRequiresOriginalScopeAndExplicitRetention(t *testing.T) {
	r := collectorRequestFixture()
	r.RetentionDays = 90
	if err := r.checkRecovery(r.Server, r.OrganizationID, r.ActorUserID, r.TargetName, 30, false); err != nil {
		t.Fatal("omitted retention replaced the original:", err)
	}
	for _, change := range []string{"server", "organization", "actor", "selector", "retention"} {
		t.Run(change, func(t *testing.T) {
			server, org, actor, slug, retention := r.Server, r.OrganizationID, r.ActorUserID, r.TargetName, r.RetentionDays
			switch change {
			case "server":
				server = "https://other.invalid"
			case "organization":
				org = uuid.NewString()
			case "actor":
				actor++
			case "selector":
				slug = "other-cluster"
			case "retention":
				retention = 30
			}
			if err := r.checkRecovery(server, org, actor, slug, retention, true); err == nil {
				t.Fatal("changed scope/selector/retention was admitted")
			}
		})
	}
}
