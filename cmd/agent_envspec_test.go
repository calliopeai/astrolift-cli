package cmd

import "testing"

func TestApplyNonRootOmitsTheFieldUnlessTheFlagIsGiven(t *testing.T) {
	input := map[string]interface{}{}
	applyNonRoot(agentEnvSpecUpsertCmd, input)
	if _, ok := input["runAsNonRoot"]; ok {
		t.Fatalf("runAsNonRoot sent without --non-root: %v", input)
	}

	if err := agentEnvSpecUpsertCmd.Flags().Set("non-root", "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = agentEnvSpecUpsertCmd.Flags().Set("non-root", "false")
		agentEnvSpecUpsertCmd.Flags().Lookup("non-root").Changed = false
	})
	applyNonRoot(agentEnvSpecUpsertCmd, input)
	if input["runAsNonRoot"] != true {
		t.Fatalf("runAsNonRoot = %v, want true", input["runAsNonRoot"])
	}
}
