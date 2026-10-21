//go:build js

// Package routersettings pkg/router/routersettings/localgraph_default_js.go
package routersettings

// localGraphDefault is dial.local_graph's default: a browser visor cannot
// afford transport discovery's whole graph, so it routes without it.
const localGraphDefault = false
