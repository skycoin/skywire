//go:build !mobile

// Package visor pkg/visor/network_feeds.go c3-vis-core
//
// A desktop visor holds two network-wide CXO feeds and answers lookups from
// them: dmsg discovery's clients-by-server feed for the dmsg server list
// (dmsg_servers_cxo.go) and the address resolver's bindings feed for
// transport addresses (ar_resolve_cxo.go).
package visor

const takesNetworkFeeds = true
