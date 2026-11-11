// Package visorapi pkg/visor/visorapi/catalog.go c3-vis-core
package visorapi

// CatalogEntry is one app a visor can run, with the instances it has
// configured. An entry with no instances is available but not configured.
type CatalogEntry struct {
	Name          string            `json:"name"`
	Kind          string            `json:"kind"`
	Description   string            `json:"description"`
	MultiInstance bool              `json:"multi_instance"`
	Instances     []CatalogInstance `json:"instances"`
}

// CatalogInstance is one configured launcher entry of a catalog app.
type CatalogInstance struct {
	Name           string `json:"name"`
	Port           uint16 `json:"port"`
	AutoStart      bool   `json:"auto_start"`
	Status         string `json:"status"`
	DetailedStatus string `json:"detailed_status,omitempty"`
}
