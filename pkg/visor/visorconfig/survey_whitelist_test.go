//go:build !mobile

package visorconfig

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The deployment survey whitelist is a policy switch with real consequences —
// its keys authorise their holders to read a visor's logs, system survey and
// pprof over dmsg — and it is read from two places that must agree: the
// generator, which writes the field, and the hourly config refresh, which
// would otherwise put back whatever generation left empty. Pinning the value
// per build is what stops one of those call sites drifting.
//
// A deployed visor is a machine someone chose to run and can legitimately
// survey, so it keeps the deployment's keys.
func TestUseDeploymentSurveyWhitelistOnDeployedBuild(t *testing.T) {
	require.True(t, UseDeploymentSurveyWhitelist(),
		"deployed visors keep the deployment's survey whitelist")
}
