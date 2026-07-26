package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestVersionCommandStampsAllFields proves the version/commit/date vars flow
// through to `astro version` output — i.e. the -ldflags injection points
// (.goreleaser.yaml, Makefile) reach the printed stamp.
func TestVersionCommandStampsAllFields(t *testing.T) {
	origV, origC, origD := Version, Commit, Date
	Version, Commit, Date = "1.2.3", "abc1234", "2026-07-25T00:00:00Z"
	defer func() { Version, Commit, Date = origV, origC, origD }()

	c := &cobra.Command{}
	out := &bytes.Buffer{}
	c.SetOut(out)
	versionCmd.Run(c, nil)

	for _, want := range []string{"1.2.3", "abc1234", "2026-07-25T00:00:00Z"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("version output missing %q:\n%s", want, out.String())
		}
	}
}

// TestVersionStringDefaultsToDevOnSourceBuild guards the "dev only on source
// builds" contract: an un-stamped `go build`/`go test` binary reports dev.
func TestVersionStringDefaultsToDevOnSourceBuild(t *testing.T) {
	if Version != "dev" {
		t.Skipf("Version stamped to %q in this build", Version)
	}
	if got := versionString(); !strings.Contains(got, "astro dev") {
		t.Errorf("source build should report dev, got %q", got)
	}
}
