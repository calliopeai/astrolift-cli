package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Offline checks catch malformed bounds and preserve authored fields. The
// server owns execution budgets, collection ranges, permissions and bindings.
func workflowBoundObject(raw map[string]interface{}, encoded *string, field string) (map[string]interface{}, error) {
	if encoded == nil {
		return raw, nil
	}
	if raw != nil {
		return nil, fmt.Errorf("supply only one of %s or %s_json", field, field)
	}
	decoder := json.NewDecoder(strings.NewReader(*encoded))
	decoder.UseNumber()
	var object map[string]interface{}
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, fmt.Errorf("%s_json must contain a JSON object", field)
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("%s_json must contain one JSON object", field)
	}
	return object, nil
}

func workflowBoundInteger(value interface{}, maximum int) bool {
	var number int64
	switch v := value.(type) {
	case int:
		number = int64(v)
	case int64:
		number = v
	case json.Number:
		var err error
		number, err = v.Int64()
		if err != nil {
			return false
		}
	default:
		return false
	}
	return number >= 1 && number <= int64(maximum)
}

func validateWorkflowBoundsShape(manifest *workflowManifest) error {
	reviewEdges := 0
	for i, stage := range manifest.Stages {
		if stage.MaxAttempts != nil && (*stage.MaxAttempts < 1 || *stage.MaxAttempts > 20) {
			return fmt.Errorf("stage[%d].max_attempts must be an integer between 1 and 20", i)
		}
		if stage.Kind == "workflow" && strings.TrimSpace(stage.Workflow) == "" {
			return fmt.Errorf("stage[%d].workflow is required for a workflow stage", i)
		}
		if stage.Kind != "workflow" && stage.Workflow != "" {
			return fmt.Errorf("stage[%d].workflow is valid only for a workflow stage", i)
		}
		edge, err := workflowBoundObject(stage.BackEdge, stage.BackEdgeJSON, "back_edge")
		if err != nil {
			return fmt.Errorf("stage[%d]: %w", i, err)
		}
		if len(edge) != 0 {
			if !workflowBoundInteger(edge["max_rounds"], 20) {
				return fmt.Errorf("stage[%d].back_edge.max_rounds must be an integer between 1 and 20", i)
			}
			target, targetOK := edge["to"].(string)
			when, whenOK := edge["when"].(string)
			if !targetOK || target == "" || !whenOK ||
				(when != "gate_rejected" && when != "stage_failed" && when != "output_equals" && when != "always") {
				return fmt.Errorf("stage[%d].back_edge requires to and a supported when condition", i)
			}
			prior := false
			for _, earlier := range manifest.Stages[:i] {
				prior = prior || earlier.OutputKey == target
			}
			if !prior || stage.OutputKey == "" {
				return fmt.Errorf("stage[%d].back_edge requires explicit source and earlier target output_key values", i)
			}
			if when == "gate_rejected" {
				if stage.Kind != "human_gate" {
					return fmt.Errorf("stage[%d]: gate_rejected requires a human_gate stage", i)
				}
				reviewEdges++
			}
			if when == "stage_failed" && stage.Kind != "agent_dispatch" && stage.Kind != "workflow" {
				return fmt.Errorf("stage[%d]: stage_failed requires an agent_dispatch or workflow stage", i)
			}
			if when == "always" && stage.Kind != "checkpoint" {
				return fmt.Errorf("stage[%d]: always requires a checkpoint control stage", i)
			}
			if when == "output_equals" {
				path, ok := edge["path"].(string)
				_, hasValue := edge["value"]
				if !ok || path == "" || !hasValue {
					return fmt.Errorf("stage[%d]: output_equals requires path and an explicit value", i)
				}
			}
			if exhaustion, exists := edge["on_exhausted"]; exists && exhaustion != "fail" && exhaustion != "escalate" && exhaustion != "continue" {
				return fmt.Errorf("stage[%d].back_edge.on_exhausted is invalid", i)
			}
		}
		iteration, err := workflowBoundObject(stage.Iteration, stage.IterationJSON, "iteration")
		if err != nil {
			return fmt.Errorf("stage[%d]: %w", i, err)
		}
		switch stage.Kind {
		case "collection":
			_, literal := iteration["items"]
			_, dynamic := iteration["items_path"]
			end, endOK := iteration["body_end"].(string)
			if !workflowBoundInteger(iteration["max_items"], 50) || !endOK || end == "" || literal == dynamic {
				return fmt.Errorf("stage[%d].iteration requires max_items (1..50), body_end and exactly one of items or items_path", i)
			}
			if dynamic {
				path, ok := iteration["items_path"].(string)
				if !ok || path == "" {
					return fmt.Errorf("stage[%d].iteration.items_path must be a field path", i)
				}
			}
		case "format_record":
			_, patternOK := iteration["pattern"].(string)
			_, separatorOK := iteration["separator"].(string)
			if iteration["source_format"] != "langflow_parser" || !patternOK || !separatorOK {
				return fmt.Errorf("stage[%d].iteration requires the langflow_parser pattern and separator contract", i)
			}
		default:
			if len(iteration) != 0 {
				return fmt.Errorf("stage[%d].iteration is valid only for collection or format_record stages", i)
			}
		}
	}
	if manifest.Workflow.Pattern == "review_loop" && reviewEdges == 0 {
		return fmt.Errorf("review_loop requires a bounded gate_rejected back_edge")
	}
	return nil
}
