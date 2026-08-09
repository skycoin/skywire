//go:build mobile

package visorconfig

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The phone's counterpart to the deployed-build assertion: a handset is
// nobody's fleet node, so the deployment's survey-whitelist keys — each of
// which could read this device's logs, survey and pprof over dmsg — are not
// written at generation and not refreshed in afterwards.
//
// If this flips, generation starts shipping the deployment's keys again AND
// the hourly config refresh starts restoring them, which is exactly the pair
// of regressions the single predicate exists to prevent.
func TestUseDeploymentSurveyWhitelistOnPhone(t *testing.T) {
	require.False(t, UseDeploymentSurveyWhitelist(),
		"the phone must default to an empty survey whitelist")
}
