// Package ui2 pkg/visor/ui2/catalog.go c3-vis-core
package ui2

import (
	"encoding/json"
	"slices"
	"strconv"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// The app catalog, as app-catalog.component.ts shows it.

// CatalogKinds is the order the groups are shown in.
var CatalogKinds = []string{"server", "client", "service", "external"}

// CatalogGroup is the catalog entries of one kind.
type CatalogGroup struct {
	Kind    string
	Entries []visorapi.CatalogEntry
}

// AppCatalog is the catalog view of a visor.
type AppCatalog struct {
	PK     string
	Groups []CatalogGroup
	Err    string
}

// CatalogOf groups the entries by kind, an unknown kind as external, and
// drops empty groups.
func CatalogOf(pk string, entries []visorapi.CatalogEntry) AppCatalog {
	c := AppCatalog{PK: pk}
	for _, k := range CatalogKinds {
		g := CatalogGroup{Kind: k}
		for _, e := range entries {
			kind := e.Kind
			if !slices.Contains(CatalogKinds, kind) {
				kind = "external"
			}
			if kind == k {
				g.Entries = append(g.Entries, e)
			}
		}
		if len(g.Entries) > 0 {
			c.Groups = append(c.Groups, g)
		}
	}
	return c
}

// NextInstanceName is the first free name-N for another instance of e.
func NextInstanceName(e visorapi.CatalogEntry) string {
	used := map[string]bool{}
	for _, i := range e.Instances {
		used[i.Name] = true
	}
	n := 2
	for used[e.Name+"-"+strconv.Itoa(n)] {
		n++
	}
	return e.Name + "-" + strconv.Itoa(n)
}

// instanceStatus is the numeric app status of a catalog status string.
func instanceStatus(s string) int {
	switch s {
	case "running":
		return 1
	case "errored":
		return 2
	case "starting":
		return 3
	}
	return 0
}

func (c AppCatalog) url(rest string) string { return VisorURL(c.PK, "apps/catalog") + rest }

// jsonVals is an hx-vals object.
func jsonVals(m map[string]string) string {
	b, _ := json.Marshal(m) //nolint:errcheck
	return string(b)
}
