//go:build !mobile

// Package visorconfig pkg/visor/visorconfig/survey_whitelist.go c3-vis-core
package visorconfig

// UseDeploymentSurveyWhitelist reports whether the deployment's survey
// whitelist — the key set the conf service hands out — should be written into
// this visor's config and refreshed into it thereafter.
//
// Those keys authorise their holders to read this visor's log server, its
// system survey and its pprof endpoints over dmsg (see initDmsgHTTPLogServer).
// On a fleet node that is the point: it is how an operator inspects machines
// they run. The mobile pair says no — see the comment there.
func UseDeploymentSurveyWhitelist() bool { return true }
