package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/viper"
)

// A JUnit report with a multi-byte character and no trailing newline: the
// digest covers UTF-8 bytes, and the content is printed exactly as stored.
const artifactTestContent = "<testsuites>\n  <testsuite name=\"unit ✓\" tests=\"1\"/>\n</testsuites>"

func artifactTestDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// artifactTestRecord is an agentTaskArtifact answer whose size and sha256
// describe digestOf, which is content unless a test wants them to disagree.
func artifactTestRecord(content, digestOf string) map[string]interface{} {
	return map[string]interface{}{
		"name": "junit.xml", "kind": "junit", "mediaType": "application/xml",
		"sizeBytes": len(digestOf), "sha256": artifactTestDigest(digestOf),
		"createdAt": "2026-11-04T10:00:00+00:00", "content": content,
	}
}

// artifactTestServer answers agentTaskArtifact with record, and the
// organization list for a whole-command run, recording every request.
func artifactTestServer(t *testing.T, record interface{}, seen *[]gqlRequest) *httptest.Server {
	t.Helper()
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		*seen = append(*seen, req)
		if strings.Contains(req.Query, "astroliftOrganizations") {
			return map[string]interface{}{"astroliftOrganizations": orgsPayload()}
		}
		return map[string]interface{}{"agentTaskArtifact": record}
	})
	t.Cleanup(srv.Close)
	return srv
}

func artifactTestClient(url string) *api.Client {
	client := api.NewClient(url, "cli-credential", false)
	client.SetOrg("org-1")
	return client
}

func TestAgentArtifactPrintsTheContentAsStored(t *testing.T) {
	var seen []gqlRequest
	srv := artifactTestServer(t, artifactTestRecord(artifactTestContent, artifactTestContent), &seen)
	cmd, out := agentTestCmd()
	if err := runAgentArtifact(cmd, context.Background(), artifactTestClient(srv.URL), "task-1", "junit.xml", ""); err != nil {
		t.Fatal(err)
	}
	if out.String() != artifactTestContent {
		t.Fatalf("stdout = %q", out.String())
	}
	if len(seen) != 1 {
		t.Fatalf("%d requests, want 1", len(seen))
	}
	if v := seen[0].Variables; v["orgId"] != "org-1" || v["taskId"] != "task-1" || v["name"] != "junit.xml" {
		t.Errorf("variables = %v", v)
	}
	for _, want := range []string{"agentTaskArtifact(orgId: $orgId, taskId: $taskId, name: $name)", "sha256", "content"} {
		if !strings.Contains(seen[0].Query, want) {
			t.Errorf("query does not carry %q:\n%s", want, seen[0].Query)
		}
	}
}

func TestAgentArtifactWritesTheFileAfterCheckingTheDigest(t *testing.T) {
	var seen []gqlRequest
	srv := artifactTestServer(t, artifactTestRecord(artifactTestContent, artifactTestContent), &seen)
	cmd, out := agentTestCmd()
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	path := filepath.Join(t.TempDir(), "junit.xml")
	if err := runAgentArtifact(cmd, context.Background(), artifactTestClient(srv.URL), "task-1", "junit.xml", path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != artifactTestContent {
		t.Fatalf("file = %q, %v", data, err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("file mode = %v, %v", info.Mode().Perm(), err)
		}
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want nothing", out.String())
	}
	want := fmt.Sprintf("Wrote junit.xml (%d bytes, sha256 %s) to %s\n", len(artifactTestContent), artifactTestDigest(artifactTestContent), path)
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestAgentArtifactRefusesContentThatDoesNotMatchItsDigest(t *testing.T) {
	// The record describes content one byte longer than what arrived.
	record := artifactTestRecord(artifactTestContent, artifactTestContent+"\n")
	for name, output := range map[string]string{"stdout": "", "file": filepath.Join(t.TempDir(), "junit.xml")} {
		t.Run(name, func(t *testing.T) {
			var seen []gqlRequest
			srv := artifactTestServer(t, record, &seen)
			cmd, out := agentTestCmd()
			err := runAgentArtifact(cmd, context.Background(), artifactTestClient(srv.URL), "task-1", "junit.xml", output)
			if err == nil || !strings.Contains(err.Error(), "does not match its sha256") {
				t.Fatalf("err = %v", err)
			}
			if out.Len() != 0 {
				t.Errorf("stdout = %q, want nothing", out.String())
			}
			if output != "" {
				if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("a refused artifact was written: %v", err)
				}
			}
		})
	}
}

func TestAgentArtifactUnknownNamePointsAtInspect(t *testing.T) {
	var seen []gqlRequest
	srv := artifactTestServer(t, nil, &seen)
	cmd, out := agentTestCmd()
	err := runAgentArtifact(cmd, context.Background(), artifactTestClient(srv.URL), "task-1", "missing.xml", "")
	if err == nil || !strings.Contains(err.Error(), `task task-1 has no artifact "missing.xml"`) || !strings.Contains(err.Error(), "astro agent inspect task-1") {
		t.Fatalf("err = %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want nothing", out.String())
	}
}

func TestAgentArtifactOnServerWithoutArtifacts(t *testing.T) {
	srv := schemaErrorServer(t, map[string]string{"agentTaskArtifact": `Cannot query field "agentTaskArtifact" on type "Query".`}, nil)
	defer srv.Close()
	cmd, _ := agentTestCmd()
	err := runAgentArtifact(cmd, context.Background(), artifactTestClient(srv.URL), "task-1", "junit.xml", "")
	if err == nil || err.Error() != "this server is older than platform #2488 and keeps no task artifacts" {
		t.Fatalf("err = %v", err)
	}
}

// A refusal that names the query is still a refusal, not an older server.
func TestAgentArtifactPassesARefusalThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"errors": []map[string]interface{}{{
			"message":    "agentTaskArtifact refused: agent.read",
			"extensions": map[string]interface{}{"code": "PERMISSION_DENIED"},
		}}})
	}))
	defer srv.Close()
	cmd, _ := agentTestCmd()
	err := runAgentArtifact(cmd, context.Background(), artifactTestClient(srv.URL), "task-1", "junit.xml", "")
	if err == nil || strings.Contains(err.Error(), "older than platform") || !strings.Contains(err.Error(), "PERMISSION_DENIED") {
		t.Fatalf("err = %v", err)
	}
}

func TestAgentArtifactJSON(t *testing.T) {
	record := artifactTestRecord(artifactTestContent, artifactTestContent)
	t.Run("record", func(t *testing.T) {
		var seen []gqlRequest
		srv := artifactTestServer(t, record, &seen)
		cmd, out := agentTestCmd()
		_ = cmd.Flags().Set("json", "true")
		if err := runAgentArtifact(cmd, context.Background(), artifactTestClient(srv.URL), "task-1", "junit.xml", ""); err != nil {
			t.Fatal(err)
		}
		var got map[string]interface{}
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("decoding %q: %v", out.String(), err)
		}
		for key, want := range map[string]interface{}{"name": "junit.xml", "kind": "junit", "mediaType": "application/xml", "sha256": artifactTestDigest(artifactTestContent), "content": artifactTestContent} {
			if got[key] != want {
				t.Errorf("%s = %v, want %v", key, got[key], want)
			}
		}
	})
	t.Run("file", func(t *testing.T) {
		var seen []gqlRequest
		srv := artifactTestServer(t, record, &seen)
		cmd, out := agentTestCmd()
		_ = cmd.Flags().Set("json", "true")
		path := filepath.Join(t.TempDir(), "junit.xml")
		if err := runAgentArtifact(cmd, context.Background(), artifactTestClient(srv.URL), "task-1", "junit.xml", path); err != nil {
			t.Fatal(err)
		}
		var got map[string]interface{}
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("decoding %q: %v", out.String(), err)
		}
		if got["path"] != path || got["sha256"] != artifactTestDigest(artifactTestContent) {
			t.Errorf("record = %v", got)
		}
		if _, ok := got["content"]; ok {
			t.Error("the record of a written file repeats its content")
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != artifactTestContent {
			t.Errorf("file = %q, %v", data, err)
		}
	})
}

// A whole run resolves the working organization before the read and hands -o
// to the run func.
func TestAgentArtifactCommandReadsInTheWorkingOrganization(t *testing.T) {
	var seen []gqlRequest
	srv := artifactTestServer(t, artifactTestRecord(artifactTestContent, artifactTestContent), &seen)
	withConfigHome(t, srv.URL)
	viper.Set("token", "cli-credential")
	t.Cleanup(func() { viper.Set("token", ""); agentArtifactOutput = "" })
	path := filepath.Join(t.TempDir(), "report.xml")

	out, err := runCmd(t, "agent", "artifact", "task-1", "junit.xml", "-o", path)
	if err != nil {
		t.Fatalf("agent artifact: %v (%s)", err, out)
	}
	var read *gqlRequest
	for i := range seen {
		if strings.Contains(seen[i].Query, "agentTaskArtifact") {
			read = &seen[i]
		}
	}
	if read == nil {
		t.Fatal("agentTaskArtifact was not sent")
	}
	if read.Organization != "org-1" || read.Variables["orgId"] != "org-1" {
		t.Errorf("organization header %q, orgId %v", read.Organization, read.Variables["orgId"])
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != artifactTestContent {
		t.Errorf("file = %q, %v", data, err)
	}
	if !strings.Contains(out, "Wrote junit.xml") {
		t.Errorf("output:\n%s", out)
	}
}

func TestAgentInspectListsArtifacts(t *testing.T) {
	artifacts := []interface{}{
		map[string]interface{}{"name": "junit.xml", "kind": "junit", "mediaType": "application/xml", "sizeBytes": 42, "sha256": strings.Repeat("a", 64), "createdAt": "2026-11-04T10:00:00+00:00"},
		map[string]interface{}{"name": "conformance-report.json", "kind": "conformance-json", "mediaType": "application/json", "sizeBytes": 2, "sha256": strings.Repeat("b", 64), "createdAt": "2026-11-04T10:00:00+00:00"},
	}
	// The stub answers artifacts only to a read that selects them.
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		task := map[string]interface{}{"id": "task-1", "status": "completed"}
		if strings.Contains(req.Query, "artifacts { name kind mediaType sizeBytes sha256 createdAt }") {
			task["artifacts"] = artifacts
		}
		return map[string]interface{}{"agentTask": task}
	})
	defer srv.Close()
	t.Cleanup(func() { agentInspectJSON = false })

	agentInspectJSON = false
	cmd, out := agentTestCmd()
	if err := runAgentInspect(cmd, context.Background(), api.NewClient(srv.URL, "cli-credential", false), &config.Config{}, "task-1"); err != nil {
		t.Fatal(err)
	}
	want := "Artifacts:\n" +
		"  junit.xml (junit, application/xml, 42 bytes) sha256:" + strings.Repeat("a", 64) + "\n" +
		"  conformance-report.json (conformance-json, application/json, 2 bytes) sha256:" + strings.Repeat("b", 64) + "\n"
	if !strings.Contains(out.String(), want) {
		t.Errorf("missing %q:\n%s", want, out.String())
	}

	agentInspectJSON = true
	cmd, out = agentTestCmd()
	if err := runAgentInspect(cmd, context.Background(), api.NewClient(srv.URL, "cli-credential", false), &config.Config{}, "task-1"); err != nil {
		t.Fatal(err)
	}
	var record struct {
		Artifacts []agentTaskArtifact `json:"artifacts"`
	}
	if err := json.Unmarshal(out.Bytes(), &record); err != nil {
		t.Fatalf("decoding %q: %v", out.String(), err)
	}
	if len(record.Artifacts) != 2 || record.Artifacts[1].Name != "conformance-report.json" || record.Artifacts[1].SizeBytes != 2 {
		t.Errorf("artifacts = %+v", record.Artifacts)
	}
}

// agentTaskReads keeps the task reads among recorded queries, leaving out the
// Zentinelle reads inspect makes afterwards.
func agentTaskReads(queries []string) []string {
	var reads []string
	for _, q := range queries {
		if strings.Contains(q, "agentTask(id: $id)") {
			reads = append(reads, q)
		}
	}
	return reads
}

func TestAgentInspectOnServerWithoutArtifacts(t *testing.T) {
	agentInspectJSON = false
	var queries []string
	srv := schemaErrorServer(t, map[string]string{"artifacts {": `Cannot query field "artifacts" on type "AstroliftAgentTask".`},
		func(query string) { queries = append(queries, query) })
	defer srv.Close()
	cmd, out := agentTestCmd()
	if err := runAgentInspect(cmd, context.Background(), api.NewClient(srv.URL, "cli-credential", false), &config.Config{}, "task-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "task-1") || strings.Contains(out.String(), "Artifacts:") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
	reads := agentTaskReads(queries)
	if len(reads) != 2 {
		t.Fatalf("%d task reads, want 2: %v", len(reads), reads)
	}
	// Leaving artifacts out keeps the brains the server does have.
	if last := reads[1]; strings.Contains(last, "artifacts {") || !strings.Contains(last, "brains {") {
		t.Errorf("fallback read:\n%s", last)
	}
}

// A server older than both refuses each unknown field in one answer, as
// GraphQL servers do.
func TestAgentInspectOnServerWithoutBrainsOrArtifacts(t *testing.T) {
	agentInspectJSON = false
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req gqlRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		queries = append(queries, req.Query)
		var refusals []map[string]string
		for _, field := range []string{"brains", "artifacts"} {
			if strings.Contains(req.Query, field+" {") {
				refusals = append(refusals, map[string]string{"message": fmt.Sprintf(`Cannot query field "%s" on type "AstroliftAgentTask".`, field)})
			}
		}
		if len(refusals) > 0 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"errors": refusals})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{
			"agentTask": map[string]interface{}{"id": "task-1", "status": "completed"},
		}})
	}))
	defer srv.Close()
	cmd, out := agentTestCmd()
	if err := runAgentInspect(cmd, context.Background(), api.NewClient(srv.URL, "cli-credential", false), &config.Config{}, "task-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "task-1") || strings.Contains(out.String(), "Artifacts:") || strings.Contains(out.String(), "Brains:") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
	reads := agentTaskReads(queries)
	if len(reads) == 0 {
		t.Fatal("no task read")
	}
	if last := reads[len(reads)-1]; strings.Contains(last, "artifacts {") || strings.Contains(last, "brains {") {
		t.Errorf("the read that answered still asks for a missing field:\n%s", last)
	}
}
