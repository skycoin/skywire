package visor

import (
	"context"
	"fmt"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// AddHypervisor adds a remote hypervisor PK and connects to it at runtime.
// The connection is not persisted — use SKYENV HYPERVISORPKS for persistence.
func (v *Visor) AddHypervisor(hvPK cipher.PubKey) error {
	if v.dmsgC == nil {
		return fmt.Errorf("DMSG client not running")
	}

	v.initLock.Lock()
	if _, ok := v.connectedHypervisors[hvPK]; ok {
		v.initLock.Unlock()
		return fmt.Errorf("already connected to hypervisor %s", hvPK)
	}
	v.initLock.Unlock()

	log := v.MasterLogger().PackageLogger("hypervisor_client").WithField("hypervisor_pk", hvPK)

	addr := dmsg.Addr{PK: hvPK, Port: skyenv.DmsgHypervisorPort}
	rpcS, err := newRPCServer(v, addr.PK.String()[:shortHashLen])
	if err != nil {
		return fmt.Errorf("failed to start RPC server for hypervisor %s: %w", hvPK, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	wg := new(sync.WaitGroup)
	wg.Add(1)

	hvErrs := make(chan error, 1)
	go func() {
		defer wg.Done()
		defer func() {
			v.initLock.Lock()
			delete(v.connectedHypervisors, hvPK)
			delete(v.hypervisorCancels, hvPK)
			v.initLock.Unlock()
		}()
		v.initLock.Lock()
		v.connectedHypervisors[hvPK] = true
		v.hypervisorCancels[hvPK] = cancel
		v.initLock.Unlock()
		ServeRPCClient(ctx, log, v.dmsgC, rpcS, addr, hvErrs)
	}()

	v.pushCloseStack("hypervisor.runtime."+hvPK.String()[:shortHashLen], func() error {
		cancel()
		wg.Wait()
		return nil
	})

	return nil
}

// RemoveHypervisor tears down a runtime-added hypervisor connection by
// PK. Only succeeds for hypervisors that were added via AddHypervisor
// — config-loaded hypervisors take a different code path and don't
// enter v.hypervisorCancels. Idempotent: returns nil if the PK isn't
// currently a runtime-added hypervisor.
//
// The connection's goroutine, on cancel, runs its deferred delete of
// hvPK from both connectedHypervisors and hypervisorCancels — so by
// the time RemoveHypervisor returns, the visor's view of "connected
// hypervisors" no longer includes hvPK.
func (v *Visor) RemoveHypervisor(hvPK cipher.PubKey) error {
	v.initLock.Lock()
	cancel, ok := v.hypervisorCancels[hvPK]
	v.initLock.Unlock()
	if !ok {
		// Not a runtime-added hypervisor (or already removed). Don't
		// error — operator's intent is satisfied either way.
		return nil
	}
	cancel()
	return nil
}

// RemoveAllHypervisors tears down every runtime-added hypervisor
// connection. Returns the count of hypervisors that were
// disconnected.
func (v *Visor) RemoveAllHypervisors() (int, error) {
	v.initLock.Lock()
	cancels := make([]context.CancelFunc, 0, len(v.hypervisorCancels))
	for _, c := range v.hypervisorCancels {
		cancels = append(cancels, c)
	}
	v.initLock.Unlock()
	for _, c := range cancels {
		c()
	}
	return len(cancels), nil
}

// SetHypervisorPassword changes the hypervisor UI's "admin" account
// password. Mirrors the /api/change-password endpoint without the
// HTTP session check — RPC is local-only and already privileged.
// Returns an error when this visor isn't hosting a hypervisor.
func (v *Visor) SetHypervisorPassword(oldPassword, newPassword string) error {
	if v.hvInstance == nil {
		return fmt.Errorf("hypervisor not running on this visor")
	}
	return v.hvInstance.users.ChangeAdminPassword(oldPassword, newPassword)
}
