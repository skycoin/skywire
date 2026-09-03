//go:build !js

package visor

// wsPageAllowed reports whether plain-ws dials are usable from this runtime.
// Native processes are not subject to browser mixed-content rules.
func wsPageAllowed() bool { return true }
