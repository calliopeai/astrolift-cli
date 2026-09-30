package cmd

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

// astro app access (astrolift-app#2132).

func TestAllowAndDenyMergeIntoTheCurrentList(t *testing.T) {
	if got := mergeAccess([]string{"veruus"}, []string{"staff", "veruus"}, nil); !reflect.DeepEqual(got, []string{"veruus", "staff"}) {
		t.Fatalf("allow = %v", got)
	}
	if got := mergeAccess([]string{"veruus", "staff"}, nil, []string{"veruus"}); !reflect.DeepEqual(got, []string{"staff"}) {
		t.Fatalf("deny = %v", got)
	}
	if got := mergeAccess(nil, nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("an empty list must be sent as [], got %v", got)
	}
}

func TestAChangeThatLocksPeopleOutNeedsConfirmation(t *testing.T) {
	appAccessYes = false
	calls := 0
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		calls++
		if strings.Contains(req.Query, "astroliftAppAccessPreview") {
			return map[string]interface{}{"astroliftAppAccessPreview": map[string]interface{}{
				"allowed": 1, "total": 2, "losing": []string{"b@example.com"},
			}}
		}
		t.Fatalf("saved without confirmation: %s", req.Query)
		return nil
	})
	defer srv.Close()
	cmd, _ := appTestCmd()
	_ = cmd.Root().PersistentFlags().Set("no-prompt", "true")

	err := runAppAccessChange(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "veruus-demo", []string{"veruus"}, []string{})

	if err == nil || !strings.Contains(err.Error(), "b@example.com") || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("want a refusal naming who loses access, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want only the preview", calls)
	}
}

func TestAConfirmedChangeIsSaved(t *testing.T) {
	appAccessYes = true
	defer func() { appAccessYes = false }()
	var saved gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "setAppAccess") {
			saved = req
			return map[string]interface{}{"setAppAccess": map[string]interface{}{
				"ok": true, "errors": []interface{}{},
				"data": map[string]interface{}{"appSlug": "veruus-demo", "groups": []string{"veruus"}, "users": []string{},
					"restricted": true, "managedByManifest": false, "enforcedOn": []string{"c"}},
			}}
		}
		return map[string]interface{}{"astroliftAppAccessPreview": map[string]interface{}{"allowed": 1, "total": 2, "losing": []string{"b@example.com"}}}
	})
	defer srv.Close()
	cmd, out := appTestCmd()

	if err := runAppAccessChange(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "veruus-demo", []string{"veruus"}, []string{}); err != nil {
		t.Fatal(err)
	}
	input := saved.Variables["input"].(map[string]interface{})
	if input["appSlug"] != "veruus-demo" || !reflect.DeepEqual(input["groups"], []interface{}{"veruus"}) {
		t.Fatalf("input = %v", input)
	}
	if !strings.Contains(out.String(), "only these may enter") {
		t.Fatalf("output = %s", out.String())
	}
}
