package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/calliopeai/astrolift-cli/internal/api"
)

const boundedCollectionManifest = `[workflow]
slug = "bounded-items"
name = "Bounded items"
pattern = "chained"
[[stage]]
kind = "collection"
output_key = "each"
iteration_json = '{"max_items":2,"body_end":"render","items":[{"text":"first"},{"text":"second"}]}'
[[stage]]
kind = "workflow"
workflow = "reviewed-body"
output_key = "nested"
max_attempts = 2
on_failure = "retry"
[[stage]]
kind = "format_record"
output_key = "render"
iteration_json = '{"source_format":"langflow_parser","pattern":"Item: {text}","separator":"\n"}'
`

func TestWorkflowBoundedContractFieldsSurviveActualTOMLRoundTrip(t *testing.T) {
	for name, source := range map[string]string{"collection": boundedCollectionManifest, "review": workflowTemplateReviewLoop} {
		t.Run(name, func(t *testing.T) {
			var original, copyManifest workflowManifest
			if _, err := toml.Decode(source, &original); err != nil {
				t.Fatal(err)
			}
			if err := validateWorkflowManifestShape(&original); err != nil {
				t.Fatal(err)
			}
			var encoded bytes.Buffer
			if err := toml.NewEncoder(&encoded).Encode(original); err != nil {
				t.Fatal(err)
			}
			if _, err := toml.Decode(encoded.String(), &copyManifest); err != nil {
				t.Fatal(err)
			}
			if err := validateWorkflowManifestShape(&copyManifest); err != nil {
				t.Fatal(err)
			}
			if name == "collection" {
				if copyManifest.Stages[1].Workflow != "reviewed-body" || *copyManifest.Stages[1].MaxAttempts != 2 || *copyManifest.Stages[0].IterationJSON != *original.Stages[0].IterationJSON {
					t.Fatalf("collection fields changed: %+v", copyManifest)
				}
			} else if copyManifest.Stages[1].BackEdge["to"] != "draft" || !workflowBoundInteger(copyManifest.Stages[1].BackEdge["max_rounds"], 3) {
				t.Fatalf("review return changed: %+v", copyManifest.Stages[1])
			}
		})
	}
}

func TestWorkflowBoundedShapeRefusesMalformedOrUnboundedContracts(t *testing.T) {
	cases := map[string]string{
		"attempt ceiling":  strings.Replace(boundedCollectionManifest, "max_attempts = 2", "max_attempts = 21", 1),
		"attempt zero":     strings.Replace(boundedCollectionManifest, "max_attempts = 2", "max_attempts = 0", 1),
		"attempt type":     strings.Replace(boundedCollectionManifest, "max_attempts = 2", "max_attempts = true", 1),
		"no nested target": strings.Replace(boundedCollectionManifest, "workflow = \"reviewed-body\"", "", 1),
		"item ceiling":     strings.Replace(boundedCollectionManifest, `"max_items":2`, `"max_items":51`, 1),
		"item type":        strings.Replace(boundedCollectionManifest, `"max_items":2`, `"max_items":2.0`, 1),
		"ambiguous source": strings.Replace(boundedCollectionManifest, `"max_items":2`, `"items_path":"items","max_items":2`, 1),
		"no body end":      strings.Replace(boundedCollectionManifest, `"body_end":"render",`, "", 1),
		"null iteration":   strings.Replace(boundedCollectionManifest, `'{"max_items":2,"body_end":"render","items":[{"text":"first"},{"text":"second"}]}'`, `'null'`, 1),
		"trailing JSON":    strings.Replace(boundedCollectionManifest, `"second"}]}'`, `"second"}]} {}'`, 1),
		"round ceiling":    strings.Replace(workflowTemplateReviewLoop, "max_rounds = 3", "max_rounds = 21", 1),
		"round type":       strings.Replace(workflowTemplateReviewLoop, "max_rounds = 3", "max_rounds = false", 1),
		"forward return":   strings.Replace(workflowTemplateReviewLoop, `to = "draft"`, `to = "review"`, 1),
		"inert pattern":    strings.Replace(workflowTemplateReviewLoop, `pattern     = "review_loop"`, `pattern     = "supervisor_worker"`, 1),
		"unbounded review": strings.Split(workflowTemplateReviewLoop, "[stage.back_edge]")[0],
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			var manifest workflowManifest
			if _, err := toml.Decode(content, &manifest); err == nil && validateWorkflowManifestShape(&manifest) == nil {
				t.Fatal("invalid bounded contract accepted offline")
			}
		})
	}
}

func TestWorkflowBoundedJSONOutputPreservesFieldsWithoutClaimingServerValidation(t *testing.T) {
	cmd, out, _ := workflowTestCmd()
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := runWorkflowValidateLocal(cmd, boundedCollectionManifest, "bounded.toml"); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{`"Workflow": "reviewed-body"`, `"MaxAttempts": 2`, `"IterationJSON"`, `"max_items\":2`} {
		if !strings.Contains(out.String(), required) {
			t.Fatalf("local metadata omitted %s: %s", required, out.String())
		}
	}
}

func TestWorkflowBoundedServerJSONRefusalRetainsEnvelopeAndReturnsFailure(t *testing.T) {
	srv := gqlServer(t, previewData(false), nil)
	defer srv.Close()
	cmd, out, _ := workflowTestCmd()
	_ = cmd.Flags().Set("json", "true")
	err := runWorkflowValidateServer(cmd, context.Background(), api.NewClient(srv.URL, "token", false), "invalid")
	if err == nil || !strings.Contains(out.String(), `"ok": false`) || !strings.Contains(out.String(), `"errorPath"`) {
		t.Fatalf("JSON validation hid refusal: %v: %s", err, out)
	}
}
