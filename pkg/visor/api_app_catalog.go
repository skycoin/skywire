// Package visor pkg/visor/api_app_catalog.go c3-vis-core
package visor

import (
	"sort"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// AppCatalog implements API.
func (v *Visor) AppCatalog() ([]visorapi.CatalogEntry, error) {
	states, err := v.Apps()
	if err != nil {
		return nil, err
	}
	return buildAppCatalog(launcher.RegisteredApps(), states), nil
}

// buildAppCatalog merges the registered apps with the configured launcher
// entries. An instance joins the entry of its name, or else of its binary,
// whichever is registered. Anything else is its own external entry.
func buildAppCatalog(registered []string, states []*appserver.AppState) []visorapi.CatalogEntry {
	entries := make(map[string]*visorapi.CatalogEntry, len(registered))
	for _, name := range registered {
		entries[name] = newCatalogEntry(name, true)
	}
	for _, st := range states {
		if st == nil {
			continue
		}
		key := st.Name
		if _, ok := entries[key]; !ok && st.Binary != "" {
			if e, ok := entries[st.Binary]; ok && e.Kind != launcher.KindExternal {
				key = st.Binary
			}
		}
		e, ok := entries[key]
		if !ok {
			e = newCatalogEntry(key, false)
			entries[key] = e
		}
		e.Instances = append(e.Instances, visorapi.CatalogInstance{
			Name:           st.Name,
			Port:           uint16(st.Port),
			AutoStart:      st.AutoStart,
			Status:         st.Status.String(),
			DetailedStatus: st.DetailedStatus,
		})
	}
	out := make([]visorapi.CatalogEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func newCatalogEntry(name string, registered bool) *visorapi.CatalogEntry {
	e := &visorapi.CatalogEntry{Name: name, Kind: launcher.KindExternal, Instances: []visorapi.CatalogInstance{}}
	info, known := launcher.AppMetadata(name)
	if known {
		e.Description = info.Description
	}
	if registered {
		e.Kind, e.MultiInstance = launcher.KindService, info.MultiInstance
		if known {
			e.Kind = info.Kind
		}
	}
	return e
}
