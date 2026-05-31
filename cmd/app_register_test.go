package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestAppManifestParse(t *testing.T) {
	cases := []struct {
		name        string
		content     string
		wantSlug    string
		wantDisplay string
		wantErr     bool
	}{
		{
			name: "valid manifest",
			content: `[app]
slug = "my-app"
display_name = "My App"
`,
			wantSlug:    "my-app",
			wantDisplay: "My App",
		},
		{
			name: "missing display_name",
			content: `[app]
slug = "my-app"
`,
			wantSlug: "my-app",
		},
		{
			name: "scaffolded defaults",
			content: `[app]
slug = "my-app"
display_name = "My App"

[[environments]]
name = "production"
`,
			wantSlug:    "my-app",
			wantDisplay: "My App",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "astrolift.toml")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatalf("writing temp manifest: %v", err)
			}

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading temp manifest: %v", err)
			}
			var m appManifest
			if _, err := toml.Decode(string(raw), &m); err != nil {
				if tc.wantErr {
					return
				}
				t.Fatalf("unexpected parse error: %v", err)
			}
			if tc.wantErr {
				t.Fatal("expected parse error, got none")
			}
			if m.App.Slug != tc.wantSlug {
				t.Errorf("slug: got %q, want %q", m.App.Slug, tc.wantSlug)
			}
			if m.App.DisplayName != tc.wantDisplay {
				t.Errorf("display_name: got %q, want %q", m.App.DisplayName, tc.wantDisplay)
			}
		})
	}
}
