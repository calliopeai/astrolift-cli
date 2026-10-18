package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

// astro brain (astrolift-app#2424).

func brainTestCmd(substrate bool) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	root := &cobra.Command{}
	root.PersistentFlags().Bool("no-prompt", false, "")
	c := &cobra.Command{Use: "test"}
	for _, name := range []string{"org", "team", "project"} {
		c.Flags().String(name, "", "")
	}
	c.Flags().Bool("no-prompt", false, "")
	c.Flags().Bool("debug", false, "")
	c.Flags().Bool("json", false, "")
	if substrate {
		addBrainSubstrateFlags(c)
	} else {
		c.Flags().IntVar(&brainHostPort, "port", 0, "")
	}
	root.AddCommand(c)
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	c.SetOut(out)
	c.SetErr(errOut)
	return c, out, errOut
}

func resetBrainFlags() {
	brainSearch, brainEnv, brainConnectEnv, brainFormat, brainWrite = "", "production", "", "mcp", ""
	brainConnectionSel, brainRepo, brainName, brainVisibility, brainTemplateRef = "", "", "", "private", ""
	brainNoDeploy, brainWait, brainYes, brainIngest, brainHostDeploy = false, false, false, false, false
	brainAccess, brainHostImage, brainHostName, brainHostSlug, brainHostPort = "viewer", "", "", "", 0
	brainAgent = ""
	brainPollInterval = time.Millisecond
}

func brainPayload(slug string) map[string]interface{} {
	return map[string]interface{}{
		"id": "b-1", "slug": slug, "name": "Acme", "kind": "mimwell", "team": "core", "project": "kb",
		"environments": []interface{}{
			map[string]interface{}{"name": "production", "url": "https://acme.example"},
			map[string]interface{}{"name": "staging", "url": "https://staging.acme.example/"},
		},
		"sharedWith": []interface{}{map[string]interface{}{"team": "sales", "access": "deployer"}},
	}
}

func brainOK(data interface{}) map[string]interface{} {
	return map[string]interface{}{"ok": true, "errors": []interface{}{}, "data": data}
}

func brainClient(srv *httptest.Server) *api.Client {
	return api.NewClient(srv.URL, "tok", false)
}

func TestBrainListShowsSharesAsTheCLINamesThem(t *testing.T) {
	resetBrainFlags()
	brainSearch = "acme"
	var seen gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		seen = req
		return map[string]interface{}{"astroliftBrains": []interface{}{brainPayload("acme-brain")}}
	})
	defer srv.Close()
	cmd, out, _ := brainTestCmd(false)

	if err := runBrainList(cmd, context.Background(), brainClient(srv)); err != nil {
		t.Fatal(err)
	}
	if seen.Variables["search"] != "acme" {
		t.Fatalf("variables = %v", seen.Variables)
	}
	if !strings.Contains(out.String(), "acme-brain") || !strings.Contains(out.String(), "sales:editor") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestBrainListJSONIsAnEmptyArrayWhenNothingIsVisible(t *testing.T) {
	resetBrainFlags()
	srv := gqlServerFunc(t, func(gqlRequest) map[string]interface{} {
		return map[string]interface{}{"astroliftBrains": nil}
	})
	defer srv.Close()
	cmd, out, _ := brainTestCmd(false)
	_ = cmd.Flags().Set("json", "true")

	if err := runBrainList(cmd, context.Background(), brainClient(srv)); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("output = %q", out.String())
	}
}

func TestBrainOnAnOlderServerSaysWhatItNeeds(t *testing.T) {
	resetBrainFlags()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errors": []interface{}{map[string]interface{}{"message": `Cannot query field "astroliftBrain" on type "Query".`}},
		})
	}))
	defer srv.Close()
	cmd, _, _ := brainTestCmd(false)

	err := runBrainShow(cmd, context.Background(), brainClient(srv), "acme-brain")
	if err == nil || !strings.Contains(err.Error(), "does not offer semantic layer management") || !strings.Contains(err.Error(), "#2424") {
		t.Fatalf("err = %v", err)
	}
}

func TestBrainShowOfAnInvisibleBrainIsNotFound(t *testing.T) {
	resetBrainFlags()
	srv := gqlServerFunc(t, func(gqlRequest) map[string]interface{} {
		return map[string]interface{}{"astroliftBrain": nil}
	})
	defer srv.Close()
	cmd, _, _ := brainTestCmd(false)

	err := runBrainShow(cmd, context.Background(), brainClient(srv), "other-brain")
	if err == nil || !strings.Contains(err.Error(), `"other-brain" not found`) {
		t.Fatalf("err = %v", err)
	}
}

func brainConnectServer(t *testing.T) *httptest.Server {
	return gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "issueBrainReadToken") {
			return map[string]interface{}{"issueBrainReadToken": brainOK(map[string]interface{}{
				"id": "b-1", "slug": "acme-brain", "header": "X-Brain-Token", "token": "jwt-1",
				"expiresAt": "2026-10-16T20:15:00Z",
				"endpoints": []string{"https://acme.example/mcp", "https://staging.acme.example/mcp"},
			})}
		}
		return map[string]interface{}{"astroliftBrain": brainPayload("acme-brain")}
	})
}

func TestBrainConnectPrintsAnMCPConfigForProduction(t *testing.T) {
	resetBrainFlags()
	srv := brainConnectServer(t)
	defer srv.Close()
	cmd, out, errOut := brainTestCmd(false)

	if err := runBrainConnect(cmd, context.Background(), brainClient(srv), "acme-brain"); err != nil {
		t.Fatal(err)
	}
	var got map[string]map[string]map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	server := got["mcpServers"]["acme-brain"]
	if server["type"] != "http" || server["url"] != "https://acme.example/mcp" {
		t.Fatalf("server = %v", server)
	}
	if h := server["headers"].(map[string]interface{}); h["X-Brain-Token"] != "jwt-1" {
		t.Fatalf("headers = %v", h)
	}
	if !strings.Contains(errOut.String(), "2026-10-16T20:15:00Z") {
		t.Fatalf("expiry not reported: %q", errOut.String())
	}
}

func TestBrainConnectEnvAndClaudeFormats(t *testing.T) {
	resetBrainFlags()
	srv := brainConnectServer(t)
	defer srv.Close()

	brainConnectEnv, brainFormat = "staging", "env"
	cmd, out, _ := brainTestCmd(false)
	if err := runBrainConnect(cmd, context.Background(), brainClient(srv), "acme-brain"); err != nil {
		t.Fatal(err)
	}
	want := "BRAIN_MCP_URL=https://staging.acme.example/mcp\nBRAIN_TOKEN_HEADER=X-Brain-Token\nBRAIN_TOKEN=jwt-1\n"
	if out.String() != want {
		t.Fatalf("env output = %q", out.String())
	}

	brainConnectEnv, brainFormat = "", "claude"
	cmd, out, _ = brainTestCmd(false)
	if err := runBrainConnect(cmd, context.Background(), brainClient(srv), "acme-brain"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "claude mcp add-json acme-brain '{") || !strings.Contains(out.String(), `"url":"https://acme.example/mcp"`) {
		t.Fatalf("claude output = %q", out.String())
	}
}

func TestBrainConnectRefusesAnUnservedEnvironmentAndAnUnknownFormat(t *testing.T) {
	resetBrainFlags()
	srv := brainConnectServer(t)
	defer srv.Close()

	brainConnectEnv = "dev"
	cmd, _, _ := brainTestCmd(false)
	if err := runBrainConnect(cmd, context.Background(), brainClient(srv), "acme-brain"); err == nil || !strings.Contains(err.Error(), `not served in "dev"`) {
		t.Fatalf("err = %v", err)
	}

	resetBrainFlags()
	brainFormat = "yaml"
	cmd, _, _ = brainTestCmd(false)
	if err := runBrainConnect(cmd, context.Background(), brainClient(srv), "acme-brain"); err == nil || !strings.Contains(err.Error(), "--format") {
		t.Fatalf("err = %v", err)
	}
}

func TestBrainConnectWritesAPrivateFile(t *testing.T) {
	resetBrainFlags()
	srv := brainConnectServer(t)
	defer srv.Close()
	brainWrite = filepath.Join(t.TempDir(), "acme.mcp.json")
	cmd, out, _ := brainTestCmd(false)

	if err := runBrainConnect(cmd, context.Background(), brainClient(srv), "acme-brain"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(brainWrite)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 && os.PathSeparator == '/' {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
	data, _ := os.ReadFile(brainWrite)
	if !strings.Contains(string(data), "jwt-1") || strings.Contains(out.String(), "jwt-1") {
		t.Fatalf("the token belongs in the file only; file=%s out=%s", data, out.String())
	}
}

func TestBrainCreateSendsTheInputAndFollowsItToReady(t *testing.T) {
	resetBrainFlags()
	brainConnectionSel, brainRepo, brainWait = "acme-gh", "acme/acme-brain", true
	var created gqlRequest
	polls := 0
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		switch {
		case strings.Contains(req.Query, "astroliftProjects"):
			return map[string]interface{}{"astroliftProjects": []interface{}{map[string]interface{}{"id": "p-1", "slug": "kb", "name": "KB"}}}
		case strings.Contains(req.Query, "astroliftSourceConnectionsPage"):
			return map[string]interface{}{"astroliftSourceConnectionsPage": map[string]interface{}{"items": []interface{}{
				map[string]interface{}{"id": "c-old", "name": "acme-gh", "isActive": false},
				map[string]interface{}{"id": "c-1", "name": "acme-gh", "isActive": true},
			}}}
		case strings.Contains(req.Query, "astroliftTeamsPage"):
			return map[string]interface{}{"astroliftTeamsPage": map[string]interface{}{"items": []interface{}{map[string]interface{}{"id": "t-1", "slug": "core"}}}}
		case strings.Contains(req.Query, "createBrain"):
			created = req
			return map[string]interface{}{"createBrain": brainOK(map[string]interface{}{"id": "cr-1", "slug": "acme-brain", "state": "PENDING", "repo": "acme/acme-brain", "templateRef": "v1"})}
		case strings.Contains(req.Query, "astroliftBrainCreation"):
			polls++
			state := "SCAFFOLDED"
			if polls > 1 {
				state = "READY"
			}
			return map[string]interface{}{"astroliftBrainCreation": map[string]interface{}{"id": "cr-1", "slug": "acme-brain", "state": state, "repo": "acme/acme-brain", "templateRef": "v1"}}
		}
		t.Fatalf("unexpected query: %s", req.Query)
		return nil
	})
	defer srv.Close()
	cmd, out, _ := brainTestCmd(true)
	_ = cmd.Flags().Set("project", "kb")
	_ = cmd.Flags().Set("team", "core")

	if err := runBrainCreate(cmd, context.Background(), brainClient(srv), &config.Config{}, "acme-brain"); err != nil {
		t.Fatal(err)
	}
	input := created.Variables["input"].(map[string]interface{})
	want := map[string]interface{}{
		"projectId": "p-1", "slug": "acme-brain", "sourceConnectionId": "c-1", "repo": "acme/acme-brain",
		"visibility": "PRIVATE", "environmentName": "production", "deploy": true, "teamId": "t-1",
	}
	if !reflect.DeepEqual(input, want) {
		t.Fatalf("input = %v\nwant    %v", input, want)
	}
	if polls != 2 || !strings.Contains(out.String(), "-> ready") || !strings.Contains(out.String(), "astro brain connect acme-brain") {
		t.Fatalf("polls = %d, output = %s", polls, out.String())
	}
}

func TestBrainCreateSendsASubstrateOnlyWhenAsked(t *testing.T) {
	resetBrainFlags()
	brainConnectionSel, brainRepo = "0b6a3d6e-6a39-4e9e-9a51-6b0c2f1f6c11", "acme/acme-brain"
	var created gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "astroliftSourceConnectionsPage") {
			t.Fatalf("a connection id is sent as given")
		}
		if strings.Contains(req.Query, "createBrain") {
			created = req
			return map[string]interface{}{"createBrain": brainOK(map[string]interface{}{"id": "cr-1", "slug": "acme-brain", "state": "PENDING", "repo": "acme/acme-brain", "templateRef": "v1"})}
		}
		return map[string]interface{}{"astroliftProjects": []interface{}{map[string]interface{}{"id": "p-1", "slug": "kb"}}}
	})
	defer srv.Close()
	cmd, _, _ := brainTestCmd(true)
	_ = cmd.Flags().Set("project", "kb")
	_ = cmd.Flags().Set("authority", "postgres")
	_ = cmd.Flags().Set("persistence", "postgres,efs")

	if err := runBrainCreate(cmd, context.Background(), brainClient(srv), &config.Config{}, "acme-brain"); err != nil {
		t.Fatal(err)
	}
	input := created.Variables["input"].(map[string]interface{})
	substrate := input["substrate"].(map[string]interface{})
	if substrate["authority"] != "POSTGRES" || substrate["blobs"] != "FS" || !reflect.DeepEqual(substrate["persistence"], []interface{}{"POSTGRES", "EFS"}) {
		t.Fatalf("substrate = %v", substrate)
	}
	if _, ok := substrate["port"]; ok {
		t.Fatalf("an unset port is left to the server: %v", substrate)
	}
	if _, ok := input["teamId"]; ok {
		t.Fatalf("no --team, no teamId: %v", input)
	}
}

func TestBrainCreateFailureCarriesTheServersReason(t *testing.T) {
	resetBrainFlags()
	brainConnectionSel, brainRepo, brainWait = "0b6a3d6e-6a39-4e9e-9a51-6b0c2f1f6c11", "acme/acme-brain", true
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		switch {
		case strings.Contains(req.Query, "createBrain"):
			return map[string]interface{}{"createBrain": brainOK(map[string]interface{}{"id": "cr-1", "slug": "acme-brain", "state": "PENDING", "repo": "acme/acme-brain", "templateRef": "v1"})}
		case strings.Contains(req.Query, "astroliftBrainCreation"):
			return map[string]interface{}{"astroliftBrainCreation": map[string]interface{}{"id": "cr-1", "slug": "acme-brain", "state": "FAILED", "repo": "acme/acme-brain", "templateRef": "v1",
				"error": map[string]interface{}{"code": "CONFLICT", "message": "acme/acme-brain already has commits", "field": "repo"}}}
		}
		return map[string]interface{}{"astroliftProjects": []interface{}{map[string]interface{}{"id": "p-1", "slug": "kb"}}}
	})
	defer srv.Close()
	cmd, _, _ := brainTestCmd(true)
	_ = cmd.Flags().Set("project", "kb")

	err := runBrainCreate(cmd, context.Background(), brainClient(srv), &config.Config{}, "acme-brain")
	if err == nil || !strings.Contains(err.Error(), "already has commits") {
		t.Fatalf("err = %v", err)
	}
}

func TestBrainShareMapsEditorToDeployerAndNeverGrantsOwner(t *testing.T) {
	resetBrainFlags()
	if got, err := brainShareAccess("editor"); err != nil || got != "DEPLOYER" {
		t.Fatalf("editor = %q, %v", got, err)
	}
	if _, err := brainShareAccess("owner"); err == nil || !strings.Contains(err.Error(), "not granted") {
		t.Fatalf("owner = %v", err)
	}

	brainAccess, brainYes = "editor", true
	var shared gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		shared = req
		return map[string]interface{}{"shareBrain": brainOK(brainPayload("acme-brain"))}
	})
	defer srv.Close()
	cmd, out, _ := brainTestCmd(false)
	_ = cmd.Flags().Set("team", "sales")

	if err := runBrainShare(cmd, context.Background(), brainClient(srv), "acme-brain"); err != nil {
		t.Fatal(err)
	}
	want := map[string]interface{}{"slug": "acme-brain", "teamSlug": "sales", "access": "DEPLOYER"}
	if !reflect.DeepEqual(shared.Variables["input"], want) {
		t.Fatalf("input = %v", shared.Variables["input"])
	}
	if !strings.Contains(out.String(), "sales (editor)") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestBrainShareAndDeleteNeedYesWithoutPrompts(t *testing.T) {
	resetBrainFlags()
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		t.Fatalf("sent without confirmation: %s", req.Query)
		return nil
	})
	defer srv.Close()

	cmd, _, _ := brainTestCmd(false)
	_ = cmd.Flags().Set("team", "sales")
	_ = cmd.Flags().Set("no-prompt", "true")
	if err := runBrainShare(cmd, context.Background(), brainClient(srv), "acme-brain"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("share err = %v", err)
	}
	cmd, _, _ = brainTestCmd(false)
	_ = cmd.Flags().Set("no-prompt", "true")
	if err := runBrainDelete(cmd, context.Background(), brainClient(srv), "acme-brain"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("delete err = %v", err)
	}
}

func TestBrainDeleteArchivesAndNamesAnApprovalRequest(t *testing.T) {
	resetBrainFlags()
	brainYes = true
	var sent gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		sent = req
		return map[string]interface{}{"archiveBrain": map[string]interface{}{"ok": false, "data": nil, "errors": []interface{}{
			map[string]interface{}{"code": "APPROVAL_REQUIRED", "message": "brain.delete needs approval", "approvalRequestId": "ar-9"},
		}}}
	})
	defer srv.Close()
	cmd, _, _ := brainTestCmd(false)

	err := runBrainDelete(cmd, context.Background(), brainClient(srv), "acme-brain")
	if !strings.Contains(sent.Query, "archiveBrain") || !reflect.DeepEqual(sent.Variables["input"], map[string]interface{}{"slug": "acme-brain"}) {
		t.Fatalf("sent = %+v", sent)
	}
	if err == nil || !strings.Contains(err.Error(), "ar-9") {
		t.Fatalf("err = %v", err)
	}
}

func TestBrainUnshareSendsTheTeam(t *testing.T) {
	resetBrainFlags()
	var sent gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		sent = req
		return map[string]interface{}{"unshareBrain": brainOK(brainPayload("acme-brain"))}
	})
	defer srv.Close()
	cmd, out, _ := brainTestCmd(false)
	_ = cmd.Flags().Set("team", "sales")

	if err := runBrainUnshare(cmd, context.Background(), brainClient(srv), "acme-brain"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sent.Variables["input"], map[string]interface{}{"slug": "acme-brain", "teamSlug": "sales"}) {
		t.Fatalf("input = %v", sent.Variables["input"])
	}
	if !strings.Contains(out.String(), "15 minutes") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestBrainRebuildIngestReportsTheRun(t *testing.T) {
	resetBrainFlags()
	brainIngest = true
	var sent gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		sent = req
		return map[string]interface{}{"rebuildBrain": brainOK(map[string]interface{}{
			"slug": "acme-brain", "mode": "ingest", "ingestRunUrl": "https://github.com/acme/acme-brain/actions/runs/1", "rebuilds": []string{"search"},
		})}
	})
	defer srv.Close()
	cmd, out, _ := brainTestCmd(false)

	if err := runBrainRebuild(cmd, context.Background(), brainClient(srv), "acme-brain"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sent.Variables["input"], map[string]interface{}{"slug": "acme-brain", "environmentName": "production", "ingest": true}) {
		t.Fatalf("input = %v", sent.Variables["input"])
	}
	if !strings.Contains(out.String(), "actions/runs/1") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestBrainRebuildWaitFollowsTheDeployment(t *testing.T) {
	resetBrainFlags()
	brainWait = true
	saved := appDeployPollInterval
	appDeployPollInterval = time.Millisecond
	defer func() { appDeployPollInterval = saved }()
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "rebuildBrain") {
			return map[string]interface{}{"rebuildBrain": brainOK(map[string]interface{}{"slug": "acme-brain", "mode": "deploy", "deploymentId": "d-1", "rebuilds": []string{}})}
		}
		return map[string]interface{}{"astroliftDeployment": map[string]interface{}{"id": "d-1", "status": "running", "environmentName": "production"}}
	})
	defer srv.Close()
	cmd, out, _ := brainTestCmd(false)

	if err := runBrainRebuild(cmd, context.Background(), brainClient(srv), "acme-brain"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Final status: running") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestBrainSubstrateKeepsWhatIsNotChanged(t *testing.T) {
	resetBrainFlags()
	brainYes = true
	var sent gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "updateBrainSubstrate") {
			sent = req
			return map[string]interface{}{"updateBrainSubstrate": brainOK(brainPayload("acme-brain"))}
		}
		b := brainPayload("acme-brain")
		b["substrate"] = map[string]interface{}{"authority": "sqlite", "blobs": "fs", "vectors": "in-db", "graph": "edges-table",
			"cell": "sqlite", "persistence": []string{"sqlite-volume"}, "replicas": 1, "image": "", "adoptedStore": "", "resources": []interface{}{}}
		return map[string]interface{}{"astroliftBrain": b}
	})
	defer srv.Close()
	cmd, _, _ := brainTestCmd(true)
	_ = cmd.Flags().Set("authority", "postgres")
	_ = cmd.Flags().Set("persistence", "sqlite-volume,postgres")

	if err := runBrainSubstrate(cmd, context.Background(), brainClient(srv), "acme-brain"); err != nil {
		t.Fatal(err)
	}
	substrate := sent.Variables["input"].(map[string]interface{})["substrate"].(map[string]interface{})
	want := map[string]interface{}{"authority": "POSTGRES", "blobs": "FS", "vectors": "IN_DB", "graph": "EDGES_TABLE",
		"persistence": []interface{}{"SQLITE_VOLUME", "POSTGRES"}, "replicas": float64(1)}
	if !reflect.DeepEqual(substrate, want) {
		t.Fatalf("substrate = %v\nwant        %v", substrate, want)
	}
}

func TestBrainHostAppSendsThePortOnlyWhenGiven(t *testing.T) {
	resetBrainFlags()
	brainHostImage = "ghcr.io/acme/bot:1"
	var sent gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "hostAppOnBrain") {
			sent = req
			return map[string]interface{}{"hostAppOnBrain": brainOK(map[string]interface{}{"brainSlug": "acme-brain", "template": "brain-app",
				"environmentName": "production", "envKeys": []string{"BRAIN_MCP_URL", "BRAIN_TOKEN"}, "app": map[string]interface{}{"id": "a-1", "slug": "acme-bot", "name": "bot"}})}
		}
		return map[string]interface{}{"astroliftProjects": []interface{}{map[string]interface{}{"id": "p-1", "slug": "kb"}}}
	})
	defer srv.Close()
	cmd, out, _ := brainTestCmd(false)
	_ = cmd.Flags().Set("project", "kb")

	if err := runBrainHostApp(cmd, context.Background(), brainClient(srv), &config.Config{}, "acme-brain"); err != nil {
		t.Fatal(err)
	}
	want := map[string]interface{}{"brainSlug": "acme-brain", "projectId": "p-1", "image": "ghcr.io/acme/bot:1"}
	if !reflect.DeepEqual(sent.Variables["input"], want) {
		t.Fatalf("input = %v", sent.Variables["input"])
	}
	if !strings.Contains(out.String(), "astro app deploy --app acme-bot") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestBrainStatusReportsFreshness(t *testing.T) {
	resetBrainFlags()
	srv := gqlServerFunc(t, func(gqlRequest) map[string]interface{} {
		return map[string]interface{}{"astroliftBrainStatus": map[string]interface{}{
			"slug": "acme-brain", "environmentName": "production", "url": "https://acme.example", "served": true,
			"deployedCommit": "aaaaaaaaaaaaaaaa", "behind": true, "compiledAt": nil, "warnings": []string{},
			"sourceHead": map[string]interface{}{"branch": "main", "commitSha": "bbbbbbbbbbbbbbbb"},
			"substrate":  map[string]interface{}{"authority": "files", "cell": "files", "persistence": []string{}, "replicas": 1, "resources": []interface{}{}},
		}}
	})
	defer srv.Close()
	cmd, out, _ := brainTestCmd(false)

	if err := runBrainStatus(cmd, context.Background(), brainClient(srv), "acme-brain"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"astro brain rebuild", "Compiled:", "never", "bbbbbbbbbbbb", "none (no knowledge-refresh.yml run)"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in\n%s", want, out.String())
		}
	}
}

func TestBrainCommandsAreScopedToTheWorkingOrganization(t *testing.T) {
	resetBrainFlags()
	var orgs []string
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		orgs = append(orgs, req.Organization)
		if strings.Contains(req.Query, "astroliftOrganizations") {
			return map[string]interface{}{"astroliftOrganizations": orgsPayload()}
		}
		return map[string]interface{}{"astroliftBrains": []interface{}{}}
	})
	defer srv.Close()
	cmd, _, _ := brainTestCmd(false)
	client := brainClient(srv)

	if _, err := resolveOrg(cmd, context.Background(), client, &config.Config{}); err != nil {
		t.Fatal(err)
	}
	if err := runBrainList(cmd, context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if orgs[len(orgs)-1] != "org-1" {
		t.Fatalf("organization headers = %v", orgs)
	}
}
