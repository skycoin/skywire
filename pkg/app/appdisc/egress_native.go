//go:build !js

// Package appdisc pkg/app/appdisc/egress_native.go c2-vis-appsvc
package appdisc

// canAdvertiseExit reports whether this build can serve as a clearnet exit —
// the capability the ServiceTypeSkysocks / ServiceTypeVPN registrations
// promise the fleet. Native builds can open arbitrary TCP/UDP egress.
const canAdvertiseExit = true
