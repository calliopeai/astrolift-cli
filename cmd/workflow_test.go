package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

// workflowTestCmd builds a command carrying the flags the workflow run-funcs
// inspect (json/debug/org/no-prompt), with stdout + stderr buffers captured.
func workflowTestCmd() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	c := &cobra.Command{}
	c.Flags().String("org", "", "")
	c.Flags().Bool("no-prompt", false, "")
	c.Flags().Bool("debug", false, "")
	c.Flags().Bool("json", false, "")
	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	c.SetOut(out)
	c.SetErr(errBuf)
	return c, out, errBuf
}

// ---- init scaffolds a parseable manifest -----------------------------------

func TestWorkflowTemplatesAreParseable(t *testing.T) {
	for pattern, tmpl := range workflowTemplates {
		t.Run(pattern, func(t *testing.T) {
			var m workflowManifest
			if _, err := toml.Decode(tmpl, &m); err != nil {
				t.Fatalf("template %q is not valid TOML: %v", pattern, err)
			}
			if err := validateWorkflowManifestShape(&m); err != nil {
				t.Fatalf("template %q fails shape validation: %v", pattern, err)
			}
			if m.Workflow.Pattern != pattern {
				t.Errorf("template %q has pattern=%q, want %q", pattern, m.Workflow.Pattern, pattern)
			}
			if len(m.Stages) == 0 {
				t.Errorf("template %q has no stages", pattern)
			}
		})
	}
}

func TestWorkflowInitWritesParseableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.toml")

	prevPattern, prevOut := workflowInitPattern, workflowInitOut
	workflowInitPattern, workflowInitOut = "chained", path
	defer func() { workflowInitPattern, workflowInitOut = prevPattern, prevOut }()

	cmd, out, _ := workflowTestCmd()
	if err := workflowInitCmd.RunE(cmd, nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out.String(), "Created "+path) {
		t.Errorf("init output missing created line:\n%s", out.String())
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading scaffolded file: %v", err)
	}
	var m workflowManifest
	if _, err := toml.Decode(string(raw), &m); err != nil {
		t.Fatalf("scaffolded file is not parseable: %v", err)
	}
	if m.Workflow.Pattern != "chained" || len(m.Stages) != 2 {
		t.Errorf("scaffolded chained manifest wrong: %+v", m)
	}

	// Refuses to overwrite an existing file.
	cmd2, _, _ := workflowTestCmd()
	if err := workflowInitCmd.RunE(cmd2, nil); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already-exists error, got %v", err)
	}
}

func TestWorkflowInitUnknownPattern(t *testing.T) {
	prevPattern, prevOut := workflowInitPattern, workflowInitOut
	workflowInitPattern, workflowInitOut = "nonsense", filepath.Join(t.TempDir(), "w.toml")
	defer func() { workflowInitPattern, workflowInitOut = prevPattern, prevOut }()

	cmd, _, _ := workflowTestCmd()
	if err := workflowInitCmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "unknown --pattern") {
		t.Fatalf("expected unknown-pattern error, got %v", err)
	}
}

// ---- local validate --------------------------------------------------------

func TestWorkflowValidateLocalOK(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "w.toml")
	if err := os.WriteFile(path, []byte(workflowTemplateChained), 0o600); err != nil {
		t.Fatal(err)
	}

	prev := workflowValidateServer
	workflowValidateServer = false
	defer func() { workflowValidateServer = prev }()

	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowValidate(cmd, path); err != nil {
		t.Fatalf("validate local: %v", err)
	}
	got := out.String()
	for _, want := range []string{"OK (local shape check)", "Feature Dev (feature-dev)", "pattern=chained", "2 stage(s)", "agent_dispatch", "human_gate"} {
		if !strings.Contains(got, want) {
			t.Errorf("local validate output missing %q:\n%s", want, got)
		}
	}
}

func TestWorkflowValidateLocalCatchesMalformed(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantSub string
	}{
		{"syntax error", "[workflow]\nslug = \nname = \"x\"\n", "parsing"},
		{"missing slug", "[workflow]\nname = \"No Slug\"\n", "slug is required"},
		{"bad pattern", "[workflow]\nslug = \"x\"\nname = \"X\"\npattern = \"spiral\"\n", "pattern \"spiral\" is invalid"},
		{"bad stage kind", "[workflow]\nslug = \"x\"\nname = \"X\"\n\n[[stage]]\nkind = \"teleport\"\n", "kind \"teleport\" is invalid"},
		{"bad fan_out", "[workflow]\nslug = \"x\"\nname = \"X\"\n\n[[stage]]\nkind = \"agent_dispatch\"\nfan_out = \"sometimes\"\n", "fan_out string must be"},
		{"duplicate explicit output key", "[workflow]\nslug = \"x\"\nname = \"X\"\n\n[[stage]]\nkind = \"agent_dispatch\"\noutput_key = \"result\"\n\n[[stage]]\nkind = \"aggregation\"\noutput_key = \"result\"\n", "output_key values must be unique"},
		{"default output key collision", "[workflow]\nslug = \"x\"\nname = \"X\"\n\n[[stage]]\nkind = \"agent_dispatch\"\n\n[[stage]]\nkind = \"aggregation\"\noutput_key = \"stage_0\"\n", "output_key values must be unique"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "w.toml")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			prev := workflowValidateServer
			workflowValidateServer = false
			defer func() { workflowValidateServer = prev }()

			cmd, _, _ := workflowTestCmd()
			err := runWorkflowValidate(cmd, path)
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("expected error containing %q, got %v", tc.wantSub, err)
			}
		})
	}
}

// ---- server validate (previewWorkflowManifest) -----------------------------

func previewData(ok bool) map[string]interface{} {
	if !ok {
		return map[string]interface{}{
			"previewWorkflowManifest": map[string]interface{}{
				"ok": false, "error": "kind must be one of [...]", "errorPath": "stage[0].kind",
				"errorLine": 9, "errorColumn": 8, "definition": nil, "stages": []interface{}{},
			},
		}
	}
	return map[string]interface{}{
		"previewWorkflowManifest": map[string]interface{}{
			"ok": true, "error": nil, "errorPath": nil, "errorLine": nil, "errorColumn": nil,
			"definition": map[string]interface{}{
				"slug": "feature-dev", "name": "Feature Dev", "pattern": "chained", "description": "Implement, then review.",
			},
			"stages": []map[string]interface{}{
				{"order": 0, "kind": "agent_dispatch", "role": "implementer", "agent": "my-coder",
					"environmentSpecSlug": "coder-prod", "outputKey": "implementation",
					"skills": []string{"write-tests"}, "onFailure": "retry", "timeout": 600, "fanOut": "0",
					"prompt": nil, "approvers": []string{}},
				{"order": 1, "kind": "human_gate", "role": "reviewer", "agent": nil,
					"skills": []string{}, "onFailure": "fail", "timeout": 86400, "fanOut": "0",
					"prompt": "Approve?", "approvers": []string{"team:reviewers"}},
			},
		},
	}
}

func TestWorkflowValidateServerHitsPreview(t *testing.T) {
	var captured gqlRequest
	srv := gqlServer(t, previewData(true), &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowValidateServer(cmd, context.Background(), client, "[workflow]\nslug=\"feature-dev\"\n"); err != nil {
		t.Fatalf("validate --server: %v", err)
	}
	if !strings.Contains(captured.Query, "previewWorkflowManifest") {
		t.Errorf("server validate did not call previewWorkflowManifest: %s", captured.Query)
	}
	if captured.Variables["toml"] != "[workflow]\nslug=\"feature-dev\"\n" {
		t.Errorf("toml var not forwarded: %v", captured.Variables["toml"])
	}
	got := out.String()
	for _, want := range []string{"OK (server validation)", "Feature Dev (feature-dev)", "pattern=chained", "agent=my-coder", "environment_spec=coder-prod", "output_key=implementation", "human_gate", "fan_out=0"} {
		if !strings.Contains(got, want) {
			t.Errorf("server validate output missing %q:\n%s", want, got)
		}
	}
}

func TestWorkflowValidateServerSurfacesParseError(t *testing.T) {
	srv := gqlServer(t, previewData(false), nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _, _ := workflowTestCmd()
	err := runWorkflowValidateServer(cmd, context.Background(), client, "bad")
	if err == nil || !strings.Contains(err.Error(), "stage[0].kind") || !strings.Contains(err.Error(), "line 9") {
		t.Fatalf("expected structured parse error surfaced, got %v", err)
	}
}

// ---- pull (exportWorkflowManifest) -----------------------------------------

const exportedTOML = "[workflow]\nslug = \"ooda\"\nname = \"OODA\"\npattern = \"chained\"\n"

func exportData(ok bool) map[string]interface{} {
	if !ok {
		return map[string]interface{}{
			"exportWorkflowManifest": map[string]interface{}{
				"ok": false, "toml": nil, "error": "no visible workflow definition with slug 'ghost'",
			},
		}
	}
	return map[string]interface{}{
		"exportWorkflowManifest": map[string]interface{}{
			"ok": true, "toml": exportedTOML, "error": nil,
		},
	}
}

func TestWorkflowPullHitsExportToStdout(t *testing.T) {
	prev := workflowPullOut
	workflowPullOut = ""
	defer func() { workflowPullOut = prev }()

	var captured gqlRequest
	srv := gqlServer(t, exportData(true), &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowPull(cmd, context.Background(), client, "ooda"); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if !strings.Contains(captured.Query, "exportWorkflowManifest") {
		t.Errorf("pull did not call exportWorkflowManifest: %s", captured.Query)
	}
	if captured.Variables["slug"] != "ooda" {
		t.Errorf("slug var = %v, want ooda", captured.Variables["slug"])
	}
	if out.String() != exportedTOML {
		t.Errorf("pull stdout = %q, want exported TOML %q", out.String(), exportedTOML)
	}
}

func TestWorkflowPullWritesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ooda.toml")
	prev := workflowPullOut
	workflowPullOut = path
	defer func() { workflowPullOut = prev }()

	srv := gqlServer(t, exportData(true), nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out, errBuf := workflowTestCmd()
	if err := runWorkflowPull(cmd, context.Background(), client, "ooda"); err != nil {
		t.Fatalf("pull -o: %v", err)
	}
	if out.String() != "" {
		t.Errorf("pull -o wrote to stdout: %q", out.String())
	}
	if !strings.Contains(errBuf.String(), "Wrote "+path) {
		t.Errorf("missing wrote-file notice on stderr: %q", errBuf.String())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading pulled file: %v", err)
	}
	if string(raw) != exportedTOML {
		t.Errorf("pulled file = %q, want %q", string(raw), exportedTOML)
	}
	// The pulled file round-trips through local validation.
	var m workflowManifest
	if _, err := toml.Decode(string(raw), &m); err != nil {
		t.Fatalf("pulled file not parseable: %v", err)
	}
	if err := validateWorkflowManifestShape(&m); err != nil {
		t.Fatalf("pulled file fails shape check: %v", err)
	}
}

func TestWorkflowPullNotFound(t *testing.T) {
	prev := workflowPullOut
	workflowPullOut = ""
	defer func() { workflowPullOut = prev }()

	srv := gqlServer(t, exportData(false), nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _, _ := workflowTestCmd()
	err := runWorkflowPull(cmd, context.Background(), client, "ghost")
	if err == nil || !strings.Contains(err.Error(), "no visible workflow definition") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}
