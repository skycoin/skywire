// Package cxoaggregator — two_aggregators_test.go: guards the regression
// where TPD's SECOND CXO aggregator (the dedicated tp-list feed on its own
// DMSG port, #4152) never accepted inbound visor announces.
//
// Root cause: cxoaggregator.New built its CXO node from node.NewConfig(),
// whose defaults bind a TCP listener on :8870 and an RPC listener on :8871.
// TPD constructs TWO aggregators in one process on ONE dmsg client (port-50
// telemetry + port-69 tp-list). The first grabbed :8870/:8871; the second's
// node.NewNode failed on "address already in use", so New returned an error
// and the tp-list aggregator was never created — it never dmsg-Listened on
// its port and never accepted the visors' tp-list announces, leaving TPD's
// transport count short of what the visors actually publish. The publisher
// path (treestore.NewWithDMSG) has always zeroed those listeners; the fix
// does the same in the aggregator.
//
// This test builds two aggregators on ONE dmsg client (the second on a
// distinct port), then drives a real visor publisher on that second port to
// AnnounceTo the TPD and publish a tp-list leaf, and asserts the SECOND
// aggregator accepted the announce and reconciled the transport into the
// sink. It fails before the fix at the second New (bind collision) and
// passes after.
package cxoaggregator

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	cxotransport "github.com/skycoin/skywire/pkg/cxo/node/transport"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

func TestTwoAggregatorsShareOneClientSecondAccepts(t *testing.T) {
	const timeout = 30 * time.Second

	env := dmsgtest.NewEnv(t, timeout)
	require.NoError(t, env.Startup(0, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	tpdPK, tpdSK := cipher.GenerateKeyPair()
	visorPK, visorSK := cipher.GenerateKeyPair()
	remotePK, _ := cipher.GenerateKeyPair() // the transport's remote edge
	_ = tpdSK

	tpdClient, err := env.NewClientWithKeys(tpdPK, tpdSK, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err, "tpd dmsg client")
	visorClient, err := env.NewClientWithKeys(visorPK, visorSK, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err, "visor dmsg client")

	sink := &recordingSink{}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// FIRST aggregator: the port-50 telemetry feed. Grabs the node's default
	// TCP(:8870)/RPC(:8871) listeners in the pre-fix code.
	agg, err := New(tpdClient, sink, Config{
		DmsgPort: cxotransport.DefaultCXOPort,
		Logger:   logging.MustGetLogger("tpd-cxo-aggregator"),
	})
	require.NoError(t, err, "first aggregator (port %d)", cxotransport.DefaultCXOPort)
	t.Cleanup(func() { _ = agg.Close() }) //nolint:errcheck
	agg.Run(ctx)

	// SECOND aggregator: the dedicated tp-list feed on its own port. This is
	// the call that returned "address already in use" before the fix, because
	// its node.NewNode tried to bind :8870 again.
	tplAgg, err := New(tpdClient, sink, Config{
		DmsgPort: skyenv.DmsgVisorTPListCXOPort,
		Logger:   logging.MustGetLogger("tpd-cxo-tplist-aggregator"),
	})
	require.NoError(t, err, "second aggregator (port %d) must construct on the shared client",
		skyenv.DmsgVisorTPListCXOPort)
	t.Cleanup(func() { _ = tplAgg.Close() }) //nolint:errcheck
	tplAgg.Run(ctx)

	// Visor-side: a dedicated tp-list publisher on the matching port, under
	// the visor's own keypair (so reporter == feed PK).
	pub, err := treestore.NewWithDMSG(visorClient, visorSK, treestore.PubConfig{
		InMemoryDB:  true,
		BatchWindow: 5 * time.Millisecond,
		DmsgPort:    skyenv.DmsgVisorTPListCXOPort,
		Logger:      logging.MustGetLogger("visor-tplist-pub"),
	})
	require.NoError(t, err, "visor tp-list publisher")
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck

	// Publish the compact tp-list leaf (same wire shape as
	// manager.publishTPDList): one transport, remote edge + type.
	body, err := json.Marshal(struct {
		Version string                   `json:"version,omitempty"`
		Compact []transport.CompactEntry `json:"c"`
	}{
		Version: "v-test",
		Compact: []transport.CompactEntry{{Remote: remotePK, Type: tptypes.STCP}},
	})
	require.NoError(t, err)
	require.NoError(t, pub.Put("tp-list", body), "publish tp-list leaf")

	// Drive the announce loop by hand: dial the TPD's tp-list aggregator over
	// dmsg. Retry until the in-process dmsg session is bridgeable.
	deadline := time.Now().Add(timeout)
	for {
		actx, acancel := context.WithTimeout(ctx, 3*time.Second)
		err = pub.AnnounceTo(actx, tpdPK)
		acancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("visor could never announce its tp-list feed to the TPD: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// The SECOND aggregator must accept the inbound announce, subscribe, fill
	// the tiny Root, and reconcile the transport into the sink — proving it is
	// actually listening on its DMSG port.
	wantID := transport.MakeTransportID(visorPK, remotePK, tptypes.STCP)
	require.Eventuallyf(t, func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		for _, rec := range sink.reconciles {
			if rec.reporter != visorPK {
				continue
			}
			for _, e := range rec.entries {
				if e.ID == wantID {
					return true
				}
			}
		}
		return false
	}, timeout, 100*time.Millisecond,
		"tp-list transport %s never reconciled through the second aggregator", wantID)
}
