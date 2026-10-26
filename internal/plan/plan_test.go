package plan

import (
	"encoding/json"
	"strings"
	"testing"
)

func project(t *testing.T, nodes []Node) (*Projection, error) {
	t.Helper()
	raw, _ := json.Marshal(map[string]interface{}{"items": nodes})
	return Decode(raw, "abc", "example")
}

func TestContainerDependenciesAndDeclaredState(t *testing.T) {
	p, err := project(t, []Node{
		{ID: "phase", Type: "phase"}, {ID: "done", Type: "story", Parent: "phase", Status: "done", Labels: []string{"repo.engine"}},
		{ID: "work", Type: "story", Status: "planned", DependsOn: []string{"phase"}, Labels: []string{"repo.web"}},
		{ID: "running", Type: "story", Status: "in-progress", Labels: []string{"repo.engine"}},
		{ID: "blocked", Type: "story", Status: "planned", DependsOn: []string{"running"}, Labels: []string{"repo.web"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	work, _ := p.Find("spec:work")
	if work.State != "ready" || work.Repo != "example/web" || len(work.Dependencies) != 1 || work.Dependencies[0] != "done" {
		t.Fatalf("%+v", work)
	}
	blocked, _ := p.Find("blocked")
	if blocked.State != "blocked" || len(blocked.BlockedBy) != 1 {
		t.Fatalf("%+v", blocked)
	}
	if len(p.Tasks) != 4 {
		t.Fatal("containers became tasks")
	}
}

func TestRejectsInvalidGraphs(t *testing.T) {
	cases := map[string][]Node{
		"duplicate":        {{ID: "a"}, {ID: "a"}},
		"unresolved":       {{ID: "a", Type: "story", DependsOn: []string{"missing"}}},
		"dependency cycle": {{ID: "a", Type: "story", DependsOn: []string{"b"}}, {ID: "b", Type: "story", DependsOn: []string{"a"}}},
		"parent cycle":     {{ID: "a", Parent: "b"}, {ID: "b", Parent: "a"}},
		"unknown parent":   {{ID: "a", Parent: "b"}},
		"no stories":       {{ID: "empty", Type: "epic"}, {ID: "a", Type: "story", DependsOn: []string{"empty"}}},
	}
	for want, nodes := range cases {
		t.Run(want, func(t *testing.T) {
			_, err := project(t, nodes)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("wanted %s: %v", want, err)
			}
		})
	}
}

func TestAmbiguousRepositoryAndUnknownStatusAreNotReady(t *testing.T) {
	p, err := project(t, []Node{{ID: "a", Type: "story", Status: "planned", Labels: []string{"repo.a", "repo.b"}}, {ID: "b", Type: "story", Status: "wishful", Labels: []string{"repo.a"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range p.Tasks {
		if task.State != "blocked" || len(task.Problems) == 0 {
			t.Fatalf("unsafe ready: %+v", task)
		}
	}
}

func TestParentDependencyAppliesToEveryStory(t *testing.T) {
	p, err := project(t, []Node{{ID: "foundation", Type: "story", Status: "planned", Labels: []string{"repo.engine"}}, {ID: "phase", Type: "phase", DependsOn: []string{"foundation"}}, {ID: "child", Type: "story", Parent: "phase", Status: "planned", Labels: []string{"repo.engine"}}})
	if err != nil {
		t.Fatal(err)
	}
	child, _ := p.Find("child")
	if child.State != "blocked" || len(child.BlockedBy) != 1 || child.BlockedBy[0] != "foundation" {
		t.Fatalf("ignored container prerequisite: %+v", child)
	}
}
