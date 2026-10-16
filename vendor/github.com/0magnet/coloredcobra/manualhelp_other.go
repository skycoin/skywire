//go:build !tinygo && !js

package coloredcobra

// templatedHelp is whether Init installs the colored usage template. See
// manualhelp_tinygo.go for the builds that render help without one.
const templatedHelp = true

// installManualHelp is a no-op where cobra's templated help is used.
func installManualHelp(_ *Config) {}
