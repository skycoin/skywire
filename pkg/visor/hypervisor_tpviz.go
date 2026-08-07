//go:build !mobile

// Package visor pkg/visor/hypervisor_tpviz.go c3-vis-core
package visor

// tpvizEnabled reports whether this build serves the network-visualizer
// backend. Desktop builds always do: the hvui shows the tab unconditionally,
// and tpviz is what stops /tp-viz/ 404ing. The mobile pair turns it off — see
// the comment at its construction site in hypervisor.go.
func tpvizEnabled() bool { return true }
