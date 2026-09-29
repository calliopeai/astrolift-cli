package cmd

import (
	"encoding/json"
	"sort"
	"testing"
)

// The wire shape of recordClusterBootstrapRunInput, pinned (#1694).
//
// Every field the CLI sent was once wrong: clusterId / releases /
// success / cloud against a server input wanting clusterSlug / status /
// installedReleases / cliVersion / hostInfo. Not one name lined up, so
// `astro cluster bootstrap` failed to record its run on every install,
// and because the write is warn-only nobody was blocked and no bootstrap
// run was ever recorded anywhere.
//
// The same list is asserted server-side against the Strawberry input in
// astrolift-app (test_bootstrap_run_contract_1694.py). Renaming a field
// on either side without the other fails one of the two.
var wantBootstrapRunFields = []string{
	"chartVersion",
	"cliVersion",
	"clusterSlug",
	"endedAt",
	"errorMessage",
	"hostInfo",
	"installedReleases",
	"startedAt",
	"status",
}

func marshalledKeys(t *testing.T, v any) []string {
	t.Helper()
	blob, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(blob, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestRecordClusterBootstrapRunInputMatchesSchema(t *testing.T) {
	// errorMessage is omitempty, so populate it to see the full shape.
	input := recordClusterBootstrapRunInput{ErrorMessage: "boom"}

	got := marshalledKeys(t, input)
	if len(got) != len(wantBootstrapRunFields) {
		t.Fatalf("field set = %v, want %v", got, wantBootstrapRunFields)
	}
	for i, want := range wantBootstrapRunFields {
		if got[i] != want {
			t.Errorf("field[%d] = %q, want %q (full set %v)", i, got[i], want, got)
		}
	}
}

func TestBootstrapRunOmitsErrorMessageOnSuccess(t *testing.T) {
	// The server takes errorMessage as nullable; a successful run should
	// leave it out rather than send an empty string that reads as "there
	// was an error and it had no message".
	got := marshalledKeys(t, recordClusterBootstrapRunInput{})
	for _, k := range got {
		if k == "errorMessage" {
			t.Fatalf("errorMessage should be omitted when empty, got %v", got)
		}
	}
}

func TestBootstrapRunStatusIsAServerEnumValue(t *testing.T) {
	// The server wants "succeeded" / "failed", not a bool. A bool was what
	// the CLI used to send under the name "success".
	for _, status := range []string{"succeeded", "failed"} {
		input := recordClusterBootstrapRunInput{Status: status}
		blob, err := json.Marshal(input)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(blob, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if _, ok := m["status"].(string); !ok {
			t.Errorf("status = %T, want string", m["status"])
		}
	}
}
