// Package transport — pkg/transport/manager_close_deregister_test.go: Close
// must not wait on TPD past its budget.
//
// The visor gives the transport manager's Close 4 s and then closes the shared
// TCP port and dmsg under it. Close deregistered every transport from TPD
// first, with 30 s to do it; over dmsg that took 7.4 s on a phone with SkySOCKS
// connected, the module timed out, and the stcpr accept loop spun on the port
// closed under it until the app crashed (G4). This hands the manager a TPD that
// never answers and checks that Close still closes the transports in time.
package transport

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	types "github.com/skycoin/skywire/pkg/transport/types"
)

// hangingTPD answers DeleteTransports only once released, whatever its
// context says: the TPD client may sit behind a request already in flight.
type hangingTPD struct {
	DiscoveryClient
	release chan struct{}
}

func (h *hangingTPD) DeleteTransports(context.Context, []uuid.UUID) (int, error) {
	<-h.release
	return 0, nil
}

func TestCloseDoesNotWaitOnAHangingTPD(t *testing.T) {
	tm := newTestManager(t)
	tpd := &hangingTPD{DiscoveryClient: tm.Conf.DiscoveryClient, release: make(chan struct{})}
	defer close(tpd.release)
	tm.Conf.DiscoveryClient = tpd

	mt := NewManagedTransportForTest(newMemTransport())
	mt.Entry = MakeEntry(tm.Conf.PubKey, mustPK(t), types.STCPR, LabelUser)
	tm.tps[mt.Entry.ID] = mt

	closed := make(chan struct{})
	start := time.Now()
	go func() {
		tm.Close()
		close(closed)
	}()
	// Well inside the visor's 4 s module timeout.
	select {
	case <-closed:
	case <-time.After(closeDeregisterTimeout + time.Second):
		t.Fatalf("Close still waiting on TPD after %v", time.Since(start))
	}
	if took := time.Since(start); took < closeDeregisterTimeout {
		t.Fatalf("Close returned after %v, before the deregistration budget ran out", took)
	}
	select {
	case <-mt.done:
	default:
		t.Fatal("Close returned without closing the transport")
	}
}
