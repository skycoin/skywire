//go:build mobile

// Package visor pkg/visor/hypervisor_tpviz_mobile.go c3-vis-core
package visor

// tpvizEnabled is false on the phone: this build embeds no hvui, so nothing
// can reach /tp-viz/, and running the backend anyway costs a startup
// geoip+cache fetch plus two permanent tickers (a ~4m30s SD/DMSG cache
// refresh over dmsg-HTTP and a 2s websocket broadcast) for a surface with no
// possible client. See the construction site in hypervisor.go for why this is
// a build tag and not the config's TPViz.Enable.
func tpvizEnabled() bool { return false }
