//go:build mobile

// Package visorconfig pkg/visor/visorconfig/survey_whitelist_mobile.go c3-vis-core
package visorconfig

// UseDeploymentSurveyWhitelist is false on the phone: the deployment's survey
// whitelist is not written at generation and not refreshed in afterwards, so
// the field defaults to empty.
//
// Those keys let their holders pull this visor's logs, its system survey and
// its pprof profiles over dmsg. On a machine an operator runs that is the
// point; a handset is not that machine. Nobody deploys a phone, the survey
// exists for reward eligibility and this build sets no reward address, so the
// keys buy the owner nothing and each one is a party that can read the device.
//
// Empty by default, not empty always. Three ways in survive: --surveywhitelist
// at generation, user_survey_whitelist at runtime (merged by
// EffectiveSurveyWhitelist and preserved across config refresh), and the
// hypervisor keys that Fleet adds — those are appended separately by
// initDmsgHTTPLogServer and are unaffected by this. The visor's own PK is
// always whitelisted, so nothing local loses access.
func UseDeploymentSurveyWhitelist() bool { return false }
