package cxoaggregator

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// TestFeedOverHandedStream: a visor's feed reaches TPD's aggregator over a
// stream handed to TPD's dmsg listener in process, as a host visor does for
// a feed that came in over skywire, with no dmsg stream between them.
func TestFeedOverHandedStream(t *testing.T) {
	const timeout = 2 * time.Minute
	env := dmsgtest.NewEnv(t, timeout)
	require.NoError(t, env.Startup(0, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	tpdPK, tpdSK := cipher.GenerateKeyPair()
	visorPK, visorSK := cipher.GenerateKeyPair()
	remotePK, _ := cipher.GenerateKeyPair()
	tpdClient, err := env.NewClientWithKeys(tpdPK, tpdSK, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	visorClient, err := env.NewClientWithKeys(visorPK, visorSK, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	sink := &recordingSink{}
	agg, err := New(tpdClient, tpdSK, sink, Config{
		DmsgPort: skyenv.DmsgVisorTPListCXOPort,
		Logger:   logging.MustGetLogger("tpd-cxo-tplist-aggregator"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = agg.Close() }) //nolint:errcheck
	agg.Run(ctx)

	var dmsgDials int
	pub, err := treestore.NewWithDMSG(visorClient, visorSK, treestore.PubConfig{
		InMemoryDB:  true,
		BatchWindow: 5 * time.Millisecond,
		DmsgPort:    skyenv.DmsgVisorTPListCXOPort,
		Logger:      logging.MustGetLogger("visor-tplist-pub"),
		Dial: func(_ context.Context, addr dmsg.Addr) (net.Conn, error) {
			ln, ok := dmsg.LocalListener(addr)
			if !ok {
				dmsgDials++
				return nil, errors.New("no listener in process")
			}
			ours, theirs := net.Pipe()
			if err := ln.Hand(theirs, dmsg.Addr{PK: visorPK, Port: 49999}); err != nil {
				return nil, err
			}
			return ours, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck

	body, err := json.Marshal(struct {
		Version string                   `json:"version,omitempty"`
		Compact []transport.CompactEntry `json:"c"`
	}{Version: "v-test", Compact: []transport.CompactEntry{{Remote: remotePK, Type: tptypes.STCP}}})
	require.NoError(t, err)
	require.NoError(t, pub.Put("tp-list", body))

	actx, acancel := context.WithTimeout(ctx, 10*time.Second)
	require.NoError(t, pub.AnnounceTo(actx, tpdPK))
	acancel()
	require.Zero(t, dmsgDials, "the dial found TPD's listener in process")

	wantID := transport.MakeTransportID(visorPK, remotePK, tptypes.STCP)
	require.Eventually(t, func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		for _, rec := range sink.reconciles {
			for _, e := range rec.entries {
				if rec.reporter == visorPK && e.ID == wantID {
					return true
				}
			}
		}
		return false
	}, timeout, 100*time.Millisecond, "the feed never reached TPD over the handed stream")
}
