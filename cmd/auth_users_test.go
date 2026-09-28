package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/pflag"
)

// astro auth-users (astrolift-app#2131).

func TestNoAuthUsersCommandTakesAPasswordAsAFlagValue(t *testing.T) {
	// A password flag lands in shell history and process listings.
	for _, c := range authUsersCmd.Commands() {
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if strings.Contains(f.Name, "password") && f.Value.Type() != "bool" {
				t.Errorf("%s --%s takes a value; passwords must come from stdin or a prompt", c.Name(), f.Name)
			}
		})
	}
}

func TestThePasswordIsReadFromStdin(t *testing.T) {
	authUsersPasswordStdin = true
	defer func() { authUsersPasswordStdin = false }()
	cmd, _ := appTestCmd()
	cmd.SetIn(strings.NewReader("Pw-123456!\n"))

	got, err := readAuthUserPassword(cmd, "")
	if err != nil || got != "Pw-123456!" {
		t.Fatalf("readAuthUserPassword = %q, %v", got, err)
	}
}

func TestAnEmptyStdinPasswordIsRefused(t *testing.T) {
	authUsersPasswordStdin = true
	defer func() { authUsersPasswordStdin = false }()
	cmd, _ := appTestCmd()
	cmd.SetIn(strings.NewReader("\n"))

	if _, err := readAuthUserPassword(cmd, ""); err == nil {
		t.Fatal("an empty password must be refused")
	}
}

func TestAMutationSendsItsInputAndSurfacesTheServersRefusal(t *testing.T) {
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"setClusterAuthUserEnabled": map[string]interface{}{
			"ok":     false,
			"errors": []map[string]interface{}{{"code": "PRECONDITION", "message": "UserNotFoundException: User does not exist."}},
		},
	}, &captured)
	defer srv.Close()

	err := runAuthUserMutation(context.Background(), api.NewClient(srv.URL, "tok", false), "setClusterAuthUserEnabled",
		map[string]interface{}{"clusterId": "c1", "username": "u1", "enabled": false})

	if err == nil || !strings.Contains(err.Error(), "User does not exist") {
		t.Fatalf("want the server's refusal, got %v", err)
	}
	input := captured.Variables["input"].(map[string]interface{})
	if input["username"] != "u1" || input["enabled"] != false {
		t.Fatalf("input = %v", input)
	}
	if !strings.Contains(captured.Query, "setClusterAuthUserEnabled") {
		t.Fatalf("query = %s", captured.Query)
	}
}
