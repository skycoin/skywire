//go:build !js || tinygo

// Package skyenv pkg/skyenv/carriers_native.go c0-com-env
package skyenv

// DefaultDmsgCarriers is the platform default for dmsg.carriers in
// freshly generated configs. Nil on native platforms (and in the
// TinyGo install-page build, which generates configs FOR native
// visors): the dmsg client's own carrier preference applies.
var DefaultDmsgCarriers []string

// DefaultHypervisorHTTPAddr is the platform default bind address for
// the hypervisor web UI.
const DefaultHypervisorHTTPAddr = ":8000"
