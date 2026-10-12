package visor

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

// Setting the visor's level also sets the process-wide one, which the
// embedded services and library packages log through.
func TestSetLogLevelSetsProcessLevel(t *testing.T) {
	prev := logging.GetLevel()
	defer logging.SetLevel(prev)

	ml := logging.NewMasterLogger()
	setLogLevel(ml, logrus.DebugLevel)
	require.Equal(t, logrus.DebugLevel, ml.GetLevel())
	require.Equal(t, logrus.DebugLevel, logging.GetLevel())

	setLogLevel(ml, logrus.InfoLevel)
	require.Equal(t, logrus.InfoLevel, ml.GetLevel())
	require.Equal(t, logrus.InfoLevel, logging.GetLevel())
}
