// Package nvkecatalog carries only the pinned command vocabulary. The server
// owns validation, permissions, native execution and the authoritative schemas.
package nvkecatalog

import (
	_ "embed"
	"encoding/json"
)

//go:embed catalog.json
var snapshot []byte

type Action struct {
	Key        string `json:"key"`
	Surface    string `json:"surface"`
	Verb       string `json:"verb"`
	Mutates    bool   `json:"mutates"`
	Permission string `json:"permission"`
}
type Catalog struct {
	Revision string   `json:"revision"`
	Actions  []Action `json:"actions"`
}

func Pinned() Catalog {
	var c Catalog
	if err := json.Unmarshal(snapshot, &c); err != nil {
		panic("invalid embedded NVKE command catalog")
	}
	return c
}
