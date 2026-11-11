//go:build mobile

// Package visor pkg/visor/network_feeds_mobile.go c3-vis-core
//
// The mobile build does not hold the network-wide CXO feeds. Decoding and
// walking every client's dmsg entry and every resolver binding made the lite
// core allocate 2.7 times what it did at G0, and held it on iOS at its
// 40 MiB limit with a GC every six seconds. Its lookups go over HTTP, as
// before the feeds were added.
package visor

const takesNetworkFeeds = false
