// Package permissioncatalog exposes permission definitions from a pinned backend
// revision. It never describes the selected server or the caller's authority.
package permissioncatalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed catalogue.json
var snapshot []byte

// Source identifies the immutable source used to generate the bundled catalogue.
type Source struct {
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
}

// Catalogue preserves the historical permissions array and adds provenance.
type Catalogue struct {
	CatalogueKind       string   `json:"catalogueKind"`
	RequiresTargetCheck bool     `json:"requiresTargetCheck"`
	Interpretation      string   `json:"interpretation"`
	Source              Source   `json:"source"`
	Permissions         []string `json:"permissions"`
}

// Read returns a fresh copy of the committed snapshot, with no network or auth.
func Read() (Catalogue, error) {
	var result Catalogue
	if err := json.Unmarshal(snapshot, &result); err != nil {
		return result, fmt.Errorf("reading bundled permission catalogue: %w", err)
	}
	return result, nil
}
