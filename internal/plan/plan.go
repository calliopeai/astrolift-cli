// Package plan projects a versioned brain manifest without dispatching or scheduling work.
package plan

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type Node struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Type      string   `json:"type"`
	Status    string   `json:"status"`
	Priority  string   `json:"priority"`
	Estimate  string   `json:"estimate"`
	Labels    []string `json:"labels"`
	DependsOn []string `json:"depends_on"`
	Parent    string   `json:"parent"`
	Path      string   `json:"path"`
}

type Task struct {
	Node
	State        string   `json:"state"`
	Repo         string   `json:"repo"`
	Dependencies []string `json:"dependencies"`
	BlockedBy    []string `json:"blocked_by"`
	Problems     []string `json:"problems"`
}

type Projection struct {
	Revision string `json:"brain_commit"`
	Tasks    []Task `json:"tasks"`
}

func Decode(data []byte, revision, owner string) (*Projection, error) {
	var manifest struct {
		Items []Node `json:"items"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decoding specs/manifest.json: %w", err)
	}
	if manifest.Items == nil {
		return nil, fmt.Errorf("manifest has no items array")
	}
	nodes := map[string]Node{}
	for _, n := range manifest.Items {
		if n.ID == "" {
			return nil, fmt.Errorf("manifest contains an empty node id")
		}
		if _, ok := nodes[n.ID]; ok {
			return nil, fmt.Errorf("duplicate spec id %q", n.ID)
		}
		nodes[n.ID] = n
	}
	for _, n := range nodes {
		if n.Parent != "" {
			if _, ok := nodes[n.Parent]; !ok {
				return nil, fmt.Errorf("%s: unknown parent %s", n.ID, n.Parent)
			}
		}
		seen := map[string]bool{}
		for at := n.ID; at != ""; at = nodes[at].Parent {
			if seen[at] {
				return nil, fmt.Errorf("parent cycle at %s", at)
			}
			seen[at] = true
		}
	}
	descendants := func(id string) []string {
		ids := []string{}
		for _, n := range nodes {
			if n.Type != "story" {
				continue
			}
			for p := n.Parent; p != ""; p = nodes[p].Parent {
				if p == id {
					ids = append(ids, n.ID)
					break
				}
			}
		}
		sort.Strings(ids)
		return ids
	}
	p := &Projection{Revision: revision, Tasks: []Task{}}
	for _, n := range manifest.Items {
		if n.Type != "story" {
			continue
		}
		task := Task{Node: n, Dependencies: []string{}, BlockedBy: []string{}, Problems: []string{}}
		deps := map[string]bool{}
		dependencyIDs := append([]string{}, n.DependsOn...)
		for parent := n.Parent; parent != ""; parent = nodes[parent].Parent {
			dependencyIDs = append(dependencyIDs, nodes[parent].DependsOn...)
		}
		for _, id := range dependencyIDs {
			dep, ok := nodes[id]
			if !ok {
				return nil, fmt.Errorf("%s: unresolved dependency %s", n.ID, id)
			}
			if dep.Type == "story" {
				deps[id] = true
			} else {
				children := descendants(id)
				if len(children) == 0 {
					return nil, fmt.Errorf("%s: dependency %s has no stories", n.ID, id)
				}
				for _, child := range children {
					deps[child] = true
				}
			}
		}
		for id := range deps {
			task.Dependencies = append(task.Dependencies, id)
			if nodes[id].Status != "done" {
				task.BlockedBy = append(task.BlockedBy, id)
			}
		}
		sort.Strings(task.Dependencies)
		sort.Strings(task.BlockedBy)
		repos := []string{}
		for _, label := range n.Labels {
			if strings.HasPrefix(label, "repo.") {
				repos = append(repos, strings.TrimPrefix(label, "repo."))
			}
		}
		if len(repos) != 1 || repos[0] == "" {
			task.Problems = append(task.Problems, "exactly one repo.<name> label is required")
		} else {
			task.Repo = owner + "/" + repos[0]
		}
		switch n.Status {
		case "done":
			task.State = "done"
		case "in-progress", "running":
			task.State = "running"
		case "planned", "ready":
			task.State = "ready"
			if len(task.BlockedBy) > 0 || len(task.Problems) > 0 {
				task.State = "blocked"
			}
		case "blocked":
			task.State = "blocked"
		case "cancelled", "canceled", "deferred", "parked":
			task.State = n.Status
		default:
			task.State = "blocked"
			task.Problems = append(task.Problems, "unrecognized source status: "+n.Status)
		}
		p.Tasks = append(p.Tasks, task)
	}
	sort.Slice(p.Tasks, func(i, j int) bool { return p.Tasks[i].ID < p.Tasks[j].ID })
	indexed := map[string]Task{}
	for _, t := range p.Tasks {
		indexed[t.ID] = t
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var walk func(string, []string) error
	walk = func(id string, path []string) error {
		if visiting[id] {
			return fmt.Errorf("dependency cycle: %s", strings.Join(append(path, id), " -> "))
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, d := range indexed[id].Dependencies {
			if err := walk(d, append(path, id)); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for _, t := range p.Tasks {
		if err := walk(t.ID, nil); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (p *Projection) Find(id string) (Task, error) {
	id = strings.TrimPrefix(id, "spec:")
	for _, t := range p.Tasks {
		if t.ID == id {
			return t, nil
		}
	}
	return Task{}, fmt.Errorf("no story with id %q", id)
}
