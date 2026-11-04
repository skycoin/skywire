package visor

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

// Setting the visor's level also sets the process-wide one, which the
// embedded services and library packages log through.
func TestSetLogLevelSetsProcessLevel(t *testing.T) {
	prev := logging.GetLevel()
	defer logging.SetLevel(prev)

	ml := logging.NewMasterLogger()
	setLogLevel(ml, logging.DebugLevel)
	require.Equal(t, logging.DebugLevel, ml.GetLevel())
	require.Equal(t, logging.DebugLevel, logging.GetLevel())

	setLogLevel(ml, logging.InfoLevel)
	require.Equal(t, logging.InfoLevel, ml.GetLevel())
	require.Equal(t, logging.InfoLevel, logging.GetLevel())
}
