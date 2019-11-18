package transport_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/snettest"
	"github.com/SkycoinProject/skywire-mainnet/pkg/transport"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	loggingLevel, ok := os.LookupEnv("TEST_LOGGING_LEVEL")
	if ok {
		lvl, err := logging.LevelFromString(loggingLevel)
		if err != nil {
			log.Fatal(err)
		}
		logger := logging.MustGetLogger("transport-test")
		dmsg.SetLogger(logger)
		logging.SetLevel(lvl)
	} else {
		logging.Disable()
	}

	os.Exit(m.Run())
}

// TODO: test hangs if Manager is closed to early, needs to receive an error though
func TestNewManager(t *testing.T) {
	tpDisc := transport.NewDiscoveryMock()

	keys := snettest.GenKeyPairs(2)
	nEnv := snettest.NewEnv(t, keys)
	defer nEnv.Teardown()

	m0, m1, tp0, tp1, err := transport.CreateTransportPair(tpDisc, keys, nEnv)
	defer func() { require.NoError(t, m0.Close()) }()
	defer func() { require.NoError(t, m1.Close()) }()

	require.NoError(t, err)
	require.NotNil(t, tp0)
	fmt.Println("transports created")

	totalSent2 := 0
	totalSent1 := 0

	// Check read/writes are of expected.
	t.Run("check_read_write", func(t *testing.T) {

		for i := 0; i < 10; i++ {
			totalSent2 += i
			rID := routing.RouteID(i)
			payload := cipher.RandByte(i)
			packet := routing.MakeDataPacket(rID, payload)
			require.NoError(t, tp1.WritePacket(context.TODO(), packet))

			recv, err := m0.ReadPacket()
			require.NoError(t, err)
			require.Equal(t, rID, recv.RouteID())
			require.Equal(t, uint16(i), recv.Size())
			require.Equal(t, payload, recv.Payload())
		}

		for i := 0; i < 20; i++ {
			totalSent1 += i
			rID := routing.RouteID(i)
			payload := cipher.RandByte(i)
			packet := routing.MakeDataPacket(rID, payload)
			require.NoError(t, tp0.WritePacket(context.TODO(), packet))

			recv, err := m1.ReadPacket()
			require.NoError(t, err)
			require.Equal(t, rID, recv.RouteID())
			require.Equal(t, uint16(i), recv.Size())
			require.Equal(t, payload, recv.Payload())
		}
	})

	// Ensure tp log entries are of expected.
	t.Run("check_tp_logs", func(t *testing.T) {

		// 1.5x log write interval just to be safe.
		time.Sleep(time.Second * 9 / 2)

		entry1, err := m0.Conf.LogStore.Entry(tp0.Entry.ID)
		require.NoError(t, err)
		assert.Equal(t, uint64(totalSent1), entry1.SentBytes)
		assert.Equal(t, uint64(totalSent2), entry1.RecvBytes)

		entry2, err := m1.Conf.LogStore.Entry(tp1.Entry.ID)
		require.NoError(t, err)
		assert.Equal(t, uint64(totalSent2), entry2.SentBytes)
		assert.Equal(t, uint64(totalSent1), entry2.RecvBytes)
	})

	// Ensure deleting a transport works as expected.
	t.Run("check_delete_tp", func(t *testing.T) {

		// Make transport ID.
		tpID := transport.MakeTransportID(m0.Conf.PubKey, m1.Conf.PubKey, "dmsg")

		// Ensure transports are registered properly in tp discovery.
		entry, err := tpDisc.GetTransportByID(context.TODO(), tpID)
		require.NoError(t, err)
		assert.Equal(t, transport.SortEdges(m0.Conf.PubKey, m1.Conf.PubKey), entry.Entry.Edges)
		assert.True(t, entry.IsUp)

		m1.DeleteTransport(tp1.Entry.ID)
		entry, err = tpDisc.GetTransportByID(context.TODO(), tpID)
		require.NoError(t, err)
		assert.False(t, entry.IsUp)
	})
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
