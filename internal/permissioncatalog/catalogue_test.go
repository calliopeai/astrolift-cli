package permissioncatalog

import (
	"encoding/hex"
	"sort"
	"strings"
	"testing"
)

func TestSnapshotProvenanceAndDefinitions(t *testing.T) {
	got, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if got.CatalogueKind != "bundled" || !got.RequiresTargetCheck || !strings.Contains(got.Interpretation, "not the selected server") {
		t.Fatal("catalogue misrepresents current server or authority")
	}
	if got.Source.Repository != "calliopeai/astrolift-app" || got.Source.Path != "backend/core/permissions.py" {
		t.Fatal("snapshot has no canonical source identity")
	}
	for value, length := range map[string]int{got.Source.Revision: 40, got.Source.SHA256: 64} {
		if _, err := hex.DecodeString(value); err != nil || len(value) != length {
			t.Fatalf("invalid source proof %q", value)
		}
	}
	if len(got.Permissions) == 0 || !sort.StringsAreSorted(got.Permissions) {
		t.Fatal("catalogue is empty or unstable")
	}
	for i, slug := range got.Permissions {
		if len(strings.Split(slug, ".")) != 2 || (i > 0 && slug == got.Permissions[i-1]) {
			t.Fatalf("invalid or repeated permission %q", slug)
		}
	}
	got.Permissions[0] = "modified.local"
	other, err := Read()
	if err != nil || other.Permissions[0] == "modified.local" {
		t.Fatal("one caller mutated another caller's catalogue")
	}
}
