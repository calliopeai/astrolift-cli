package cmd

import (
	"strconv"
	"strings"
	"testing"
)

func stagePtr(s string) *string { return &s }

func previewStage(order int, kind string, agent *string) workflowPreviewStage {
	return workflowPreviewStage{Order: order, Kind: kind, Agent: agent}
}

func wl(slug, name, guid string) agentWorkload {
	return agentWorkload{ID: guid, Slug: slug, Name: name}
}

// The whole point of the command: a manifest naming its agents resolves to
// GUIDs with no operator lookup.
func TestResolveStageBindingsFromManifestAgents(t *testing.T) {
	stages := []workflowPreviewStage{
		previewStage(0, agentDispatchStageKind, stagePtr("triage-bot")),
		previewStage(1, "human_gate", nil),
		previewStage(2, agentDispatchStageKind, stagePtr("fix-bot")),
	}
	workloads := []agentWorkload{
		wl("triage-bot", "Triage Bot", "guid-triage"),
		wl("fix-bot", "Fix Bot", "guid-fix"),
	}

	got, err := resolveBindingsWithWorkloads(stages, nil, workloads)
	if err != nil {
		t.Fatalf("resolveStageBindings: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected only the 2 agent_dispatch stages, got %d: %+v", len(got), got)
	}
	if got[0].Order != 0 || got[0].GUID != "guid-triage" || got[0].Source != "manifest" {
		t.Errorf("stage 0 binding wrong: %+v", got[0])
	}
	if got[1].Order != 2 || got[1].GUID != "guid-fix" {
		t.Errorf("stage 2 binding wrong: %+v", got[1])
	}
}

// A non-agent stage must never be bound — binding a human_gate would
// misconfigure the workflow.
func TestResolveStageBindingsSkipsNonAgentStages(t *testing.T) {
	stages := []workflowPreviewStage{
		previewStage(0, "human_gate", nil),
		previewStage(1, "notify", nil),
	}
	got, err := resolveBindingsWithWorkloads(stages, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no bindings, got %+v", got)
	}
}

func TestResolveStageBindingsOverrideWins(t *testing.T) {
	stages := []workflowPreviewStage{previewStage(0, agentDispatchStageKind, stagePtr("triage-bot"))}
	workloads := []agentWorkload{wl("triage-bot", "Triage Bot", "guid-triage")}
	overrides, err := parseStageBindings([]string{"0=guid-override"})
	if err != nil {
		t.Fatalf("parseStageBindings: %v", err)
	}

	got, err := resolveBindingsWithWorkloads(stages, overrides, workloads)
	if err != nil {
		t.Fatalf("resolveStageBindings: %v", err)
	}
	if got[0].GUID != "guid-override" || got[0].Source != "--bind" {
		t.Errorf("--bind should win over the manifest agent: %+v", got[0])
	}
}

// A manifest written against another org names agents that do not exist
// here; the error must be actionable rather than a bare failure.
func TestResolveStageBindingsUnknownAgentListsCandidates(t *testing.T) {
	stages := []workflowPreviewStage{previewStage(0, agentDispatchStageKind, stagePtr("ghost-bot"))}
	workloads := []agentWorkload{wl("triage-bot", "Triage Bot", "guid-triage")}

	_, err := resolveBindingsWithWorkloads(stages, nil, workloads)
	if err == nil {
		t.Fatal("expected an error for an unregistered agent")
	}
	msg := err.Error()
	for _, want := range []string{"ghost-bot", "triage-bot", "--bind 0="} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should mention %q, got: %s", want, msg)
		}
	}
}

func TestResolveStageBindingsAgentStageWithoutAgentErrors(t *testing.T) {
	stages := []workflowPreviewStage{previewStage(3, agentDispatchStageKind, nil)}
	_, err := resolveBindingsWithWorkloads(stages, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "names no agent") {
		t.Fatalf("expected a names-no-agent error, got: %v", err)
	}
}

// A --bind pointed at a stage order the manifest doesn't have is a typo;
// silently ignoring it would configure a workflow the caller didn't ask for.
func TestResolveStageBindingsRejectsStrayOverride(t *testing.T) {
	stages := []workflowPreviewStage{previewStage(0, agentDispatchStageKind, stagePtr("triage-bot"))}
	workloads := []agentWorkload{wl("triage-bot", "Triage Bot", "guid-triage")}
	overrides, _ := parseStageBindings([]string{"0=guid-a", "7=guid-b"})

	_, err := resolveBindingsWithWorkloads(stages, overrides, workloads)
	if err == nil || !strings.Contains(err.Error(), "7") {
		t.Fatalf("expected a stray-override error naming stage 7, got: %v", err)
	}
}

// Slug is the identifier `agent dispatch` takes, so an exact slug match must
// beat a name match when both could apply.
func TestMatchAgentWorkloadPrefersSlugOverName(t *testing.T) {
	workloads := []agentWorkload{
		wl("other", "triage-bot", "guid-by-name"),
		wl("triage-bot", "Triage Bot", "guid-by-slug"),
	}
	guid, err := matchAgentWorkload("triage-bot", workloads, 0)
	if err != nil {
		t.Fatalf("matchAgentWorkload: %v", err)
	}
	if guid != "guid-by-slug" {
		t.Errorf("slug match should win, got %q", guid)
	}
}

func TestMatchAgentWorkloadFallsBackToNameCaseInsensitively(t *testing.T) {
	workloads := []agentWorkload{wl("triage-bot", "Triage Bot", "guid-triage")}
	guid, err := matchAgentWorkload("triage bot", workloads, 0)
	if err != nil {
		t.Fatalf("matchAgentWorkload: %v", err)
	}
	if guid != "guid-triage" {
		t.Errorf("name match = %q", guid)
	}
}

func TestMatchAgentWorkloadEmptyOrgGivesRegistrationHint(t *testing.T) {
	_, err := matchAgentWorkload("triage-bot", nil, 1)
	if err == nil || !strings.Contains(err.Error(), "none registered") {
		t.Fatalf("expected a registration hint, got: %v", err)
	}
}

func TestBindingsToStagePayloadShape(t *testing.T) {
	payload := bindingsToStagePayload([]stageBinding{
		{Order: 0, GUID: "guid-a"},
		{Order: 2, GUID: "guid-b"},
	})
	// Keys are stage orders as strings, matching what createWorkflow validates.
	for order, guid := range map[string]string{"0": "guid-a", "2": "guid-b"} {
		entry, ok := payload[order].(map[string]interface{})
		if !ok {
			t.Fatalf("missing stage %s in payload: %#v", order, payload)
		}
		if entry["agent_workload_id"] != guid {
			t.Errorf("stage %s = %v, want %s", order, entry["agent_workload_id"], guid)
		}
	}
	if bindingsToStagePayload(nil) != nil {
		t.Error("no bindings should produce a nil payload, not an empty object")
	}
}

func TestPrintStageBindingsRendersSourceColumn(t *testing.T) {
	var sb strings.Builder
	printStageBindings(&sb, []stageBinding{
		{Order: 0, Agent: "triage-bot", GUID: "guid-triage", Source: "manifest"},
		{Order: 1, Agent: "fix-bot", GUID: "guid-override", Source: "--bind"},
	})
	got := sb.String()
	for _, want := range []string{"triage-bot", "guid-triage", "manifest", "--bind"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q:\n%s", want, got)
		}
	}
}

func TestPrintStageBindingsHandlesNoAgentStages(t *testing.T) {
	var sb strings.Builder
	printStageBindings(&sb, nil)
	if !strings.Contains(sb.String(), "No agent_dispatch stages") {
		t.Errorf("unexpected empty rendering: %q", sb.String())
	}
}

// Guards the contract parseStageBindings and resolveStageBindings share: the
// override map is keyed by the stage order rendered as a string.
func TestStageOverrideKeyMatchesStageOrder(t *testing.T) {
	overrides, err := parseStageBindings([]string{"12=guid-x"})
	if err != nil {
		t.Fatalf("parseStageBindings: %v", err)
	}
	if _, ok := overrides[strconv.Itoa(12)]; !ok {
		t.Errorf("override key should be the stage order as a string: %#v", overrides)
	}
}
