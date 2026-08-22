// Package skills vendors the astrolift-skills catalogue into the astro
// binary via go:embed.
//
// Embedded rather than fetched: an agent onboarding itself is exactly the
// caller least able to clone a private repo, and often has no network at
// all. Refresh with `make vendor-skills`; `make vendor-skills-check` fails
// the build when the committed copy drifts from the source.
package skills

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed all:catalogue
var embedded embed.FS

const root = "catalogue"

// Skill is one entry of the catalogue, as authored in catalogue.json.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	// Path is the catalogue-relative directory holding the skill, e.g.
	// "skills/astro-cli".
	Path string `json:"path"`
}

// Catalogue returns every embedded skill, ordered by name so output is
// stable across runs and platforms (embed.FS walk order is not a contract
// callers should depend on).
func Catalogue() ([]Skill, error) {
	raw, err := embedded.ReadFile(path.Join(root, "catalogue.json"))
	if err != nil {
		return nil, fmt.Errorf("reading embedded catalogue: %w", err)
	}
	var out []Skill
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parsing embedded catalogue: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Lookup returns the named skill, or false when the catalogue has no such
// entry.
func Lookup(name string) (Skill, bool) {
	all, err := Catalogue()
	if err != nil {
		return Skill{}, false
	}
	for _, s := range all {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

// File is one file belonging to a skill, with its path relative to the
// skill's own directory.
type File struct {
	RelPath string
	Content []byte
}

// Files returns every file under a skill's directory.
//
// Refuses a catalogue entry whose path escapes the embedded tree. The
// catalogue is committed source rather than user input, but this function
// writes to whatever directory the caller names, so a traversal here would
// be a write outside the target -- cheap to refuse, expensive to discover.
func Files(s Skill) ([]File, error) {
	rel := strings.TrimPrefix(path.Clean(s.Path), "./")
	if rel == "" || rel == "." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
		return nil, fmt.Errorf("skill %q has an unusable path %q", s.Name, s.Path)
	}

	dir := path.Join(root, rel)
	var out []File
	err := fs.WalkDir(embedded, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		body, err := embedded.ReadFile(p)
		if err != nil {
			return err
		}
		trimmed := strings.TrimPrefix(strings.TrimPrefix(p, dir), "/")
		out = append(out, File{RelPath: trimmed, Content: body})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking skill %q: %w", s.Name, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RelPath < out[j].RelPath })
	return out, nil
}
