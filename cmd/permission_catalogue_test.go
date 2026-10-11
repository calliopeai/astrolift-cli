package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/permissioncatalog"
	"github.com/spf13/cobra"
)

type catalogueNetworkBarrier struct{ calls int }

func (b *catalogueNetworkBarrier) RoundTrip(_ *http.Request) (*http.Response, error) {
	b.calls++
	return nil, errors.New("offline permission list attempted network access")
}

func TestPermissionCatalogueIsOfflineWithAdditiveJSONProvenance(t *testing.T) {
	barrier := &catalogueNetworkBarrier{}
	previousTransport, previousVersion := http.DefaultTransport, Version
	http.DefaultTransport, Version = barrier, "v0.99.0"
	t.Cleanup(func() { http.DefaultTransport, Version = previousTransport, previousVersion })
	t.Setenv("GH_TOKEN", "release-hint-must-not-use-this")
	t.Setenv("ASTROLIFT_TOKEN", "account-token-must-not-enter-catalogue")
	t.Setenv("ASTROLIFT_NO_UPDATE_CHECK", "0")
	for _, asJSON := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[asJSON], func(t *testing.T) {
			out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
			parent := &cobra.Command{Use: "astro", PersistentPostRun: func(command *cobra.Command, _ []string) { maybeNotifyStale(command) }}
			command := *permsListCmd
			command.ResetFlags()
			command.Flags().Bool("json", asJSON, "")
			parent.AddCommand(&command)
			parent.SetOut(out)
			parent.SetErr(errOut)
			parent.SetArgs([]string{"list"})
			if err := parent.Execute(); err != nil {
				t.Fatal(err)
			}
			if barrier.calls != 0 || errOut.Len() != 0 || strings.Contains(out.String(), "must-not") {
				t.Fatalf("offline catalogue performed I/O or exposed credentials: %d calls, %s", barrier.calls, errOut)
			}
			if asJSON {
				// Existing consumers can still unmarshal exactly permissions[].
				var legacy struct {
					Permissions []string `json:"permissions"`
				}
				if err := json.Unmarshal(out.Bytes(), &legacy); err != nil || len(legacy.Permissions) != 102 {
					t.Fatalf("legacy JSON array contract changed: %v", err)
				}
				var full permissioncatalog.Catalogue
				if err := json.Unmarshal(out.Bytes(), &full); err != nil {
					t.Fatal(err)
				}
				if full.CatalogueKind != "bundled" || !full.RequiresTargetCheck || full.Source.Revision != "20cfdf2f4a8df9f4a3546f2d4a09b61f4d4e3ee4" {
					t.Fatal("JSON omits offline provenance or authority limits")
				}
				for _, slug := range []string{"workflow.trigger", "project.read", "org.manage_members", "agent.dispatch", "cluster.users", "app.exec_pod"} {
					if !strings.Contains(out.String(), `"`+slug+`"`) {
						t.Fatalf("published source definition omitted: %s", slug)
					}
				}
				for _, obsolete := range []string{"org.member.invite", "app.secret.read", "app.exec", "cluster.delete", "cluster.observe"} {
					if strings.Contains(out.String(), `"`+obsolete+`"`) {
						t.Fatalf("obsolete invented permission survived: %s", obsolete)
					}
				}
			} else if !strings.Contains(out.String(), "Bundled permission definitions") || !strings.Contains(out.String(), "not the selected server") || strings.Contains(out.String(), " scope)") {
				t.Fatal("text claims server authority or confuses resource categories with grant scopes")
			}
		})
	}
}
