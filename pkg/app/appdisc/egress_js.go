//go:build js

// Package appdisc pkg/app/appdisc/egress_js.go c2-vis-appsvc
package appdisc

// canAdvertiseExit is false on js/wasm: a browser cannot open arbitrary
// TCP/UDP connections, so a wasm visor's skysocks/VPN server has no clearnet
// egress and must not register as an exit — peers that picked one from
// service discovery formed the route, then every clearnet request failed
// (the "zombie exit" the honest-probe machinery exists to weed out; better
// not to enter the pool at all). The mesh-side skysocks server still works
// for in-mesh targets; it just is not advertised as an exit.
const canAdvertiseExit = false
