package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
)

// contextPack follows only the context recipe declared in this pinned brain.
func contextPack(source *planSource, id string) (map[string]interface{}, error) {
	raw, err := source.read("app/brain.json")
	if err != nil {
		return nil, err
	}
	var graph struct {
		Nodes    []map[string]interface{} `json:"nodes"`
		Edges    []map[string]interface{} `json:"edges"`
		Ontology json.RawMessage          `json:"ontology"`
	}
	if err := json.Unmarshal(raw, &graph); err != nil {
		return nil, fmt.Errorf("invalid compiled brain: %w", err)
	}
	raw, err = source.read("brain-schema.json")
	if err != nil {
		return nil, err
	}
	type hop struct {
		Rel        string `json:"rel"`
		Direction  string `json:"direction"`
		As         string `json:"as"`
		Transitive bool   `json:"transitive"`
	}
	var schema struct {
		Recipes map[string]json.RawMessage `json:"contextRecipes"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	nodes := map[string]map[string]interface{}{}
	for _, n := range graph.Nodes {
		if nid, ok := n["id"].(string); ok {
			nodes[nid] = n
		}
	}
	node := nodes["spec:"+id]
	if node == nil {
		return nil, fmt.Errorf("compiled brain lacks spec:%s; regenerate it before dispatch", id)
	}
	kind, _ := node["kind"].(string)
	recipeBytes, ok := schema.Recipes[kind]
	if !ok {
		recipeBytes = schema.Recipes["default"]
	}
	var recipe struct {
		Hops []hop `json:"hops"`
	}
	if len(recipeBytes) > 0 {
		if err := json.Unmarshal(recipeBytes, &recipe); err != nil {
			return nil, fmt.Errorf("invalid selected context recipe: %w", err)
		}
	}
	pack := map[string]interface{}{"version": "1", "generated_for": "spec:" + id, "node": node}
	for _, h := range recipe.Hops {
		if h.As == "" || h.Rel == "" {
			return nil, fmt.Errorf("invalid context recipe hop")
		}
		if _, exists := pack[h.As]; exists {
			return nil, fmt.Errorf("context recipe overwrites %s", h.As)
		}
		seen := map[string]bool{"spec:" + id: true}
		frontier := []string{"spec:" + id}
		found := map[string]interface{}{}
		for len(frontier) > 0 {
			next := []string{}
			for _, at := range frontier {
				for _, edge := range graph.Edges {
					if edge["rel"] != h.Rel {
						continue
					}
					from, to := edge["source"], edge["target"]
					if h.Direction == "in" {
						from, to = to, from
					}
					if from != at {
						continue
					}
					target, ok := to.(string)
					if !ok || seen[target] {
						continue
					}
					seen[target] = true
					if n := nodes[target]; n != nil {
						found[target] = n
						next = append(next, target)
					} else {
						found[target] = target
					}
				}
			}
			if !h.Transitive {
				break
			}
			frontier = next
		}
		keys := make([]string, 0, len(found))
		for k := range found {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		values := []interface{}{}
		for _, k := range keys {
			values = append(values, found[k])
		}
		pack[h.As] = values
	}
	selected := map[string]bool{"spec:" + id: true}
	for _, value := range pack {
		if items, ok := value.([]interface{}); ok {
			for _, item := range items {
				if node, ok := item.(map[string]interface{}); ok {
					if key, ok := node["id"].(string); ok {
						selected[key] = true
					}
				}
				if key, ok := item.(string); ok {
					selected[key] = true
				}
			}
		}
	}
	evidence := []map[string]interface{}{}
	for _, edge := range graph.Edges {
		from, _ := edge["source"].(string)
		to, _ := edge["target"].(string)
		if edge["evidence"] != nil && selected[from] && selected[to] {
			evidence = append(evidence, edge)
		}
	}
	sort.Slice(evidence, func(i, j int) bool {
		a, _ := json.Marshal(evidence[i])
		b, _ := json.Marshal(evidence[j])
		return string(a) < string(b)
	})
	if len(evidence) > 0 {
		pack["evidence_edges"] = evidence
	}
	if len(graph.Ontology) > 0 {
		pack["source_ontology"] = graph.Ontology
	}
	return pack, nil
}
