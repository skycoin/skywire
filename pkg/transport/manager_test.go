package transport_test

import (
	"io"
	"log"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

var masterLogger *logging.MasterLogger

func TestMain(m *testing.M) {
	masterLogger = logging.NewMasterLogger()
	loggingLevel, ok := os.LookupEnv("TEST_LOGGING_LEVEL")
	if ok {
		lvl, err := logging.LevelFromString(loggingLevel)
		if err != nil {
			log.Fatal(err)
		}
		masterLogger.SetLevel(lvl)
	} else {
		masterLogger.Out = io.Discard
	}

	os.Exit(m.Run())
}
func TestSortEdges(t *testing.T) {
	for i := 0; i < 100; i++ {
		keyA, _ := cipher.GenerateKeyPair()
		keyB, _ := cipher.GenerateKeyPair()
		require.Equal(t, transport.SortEdges(keyA, keyB), transport.SortEdges(keyB, keyA))
	}
}

func TestMakeTransportID(t *testing.T) {
	t.Run("id_is_stable", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			keyA, _ := cipher.GenerateKeyPair()
			keyB, _ := cipher.GenerateKeyPair()
			idAB := transport.MakeTransportID(keyA, keyB, "type")
			idBA := transport.MakeTransportID(keyB, keyA, "type")
			require.Equal(t, idAB, idBA)
		}
	})
	t.Run("tpType_changes_id", func(t *testing.T) {
		keyA, _ := cipher.GenerateKeyPair()
		require.NotEqual(t, transport.MakeTransportID(keyA, keyA, "a"), transport.MakeTransportID(keyA, keyA, "b"))
	})
}
