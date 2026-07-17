// Package visor pkg/visor/api_hypervisors.go c3-vis-core
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
		ServeRPCClient(ctx, log, v.tpM, v.dmsgC, rpcS, addr, hvErrs)
	}()

	v.pushCloseStack("hypervisor.runtime."+hvPK.String()[:shortHashLen], func() error {
		cancel()
		wg.Wait()
		return nil
	})

	// Persist so the change survives a restart (the operator is configuring this
	// hypervisor, not just connecting once). Best-effort — see persistHypervisors.
	v.persistHypervisors(addPK(v.configuredHypervisors(), hvPK))

	return nil
}

// configuredHypervisors returns a copy of the visor's configured hypervisor PKs.
// Unlocked read of v.conf.Hypervisors, matching the codebase's config-update
// convention (UpdateMinHops etc. mutate under v1.mu; readers don't lock — the
// writes are rare operator actions).
func (v *Visor) configuredHypervisors() []cipher.PubKey {
	if v.conf == nil {
		return nil
	}
	return append([]cipher.PubKey(nil), v.conf.Hypervisors...)
}

// addPK returns pks with pk appended if not already present.
func addPK(pks []cipher.PubKey, pk cipher.PubKey) []cipher.PubKey {
	for _, p := range pks {
		if p == pk {
			return pks
		}
	}
	return append(pks, pk)
}

// removePK returns pks without pk.
func removePK(pks []cipher.PubKey, pk cipher.PubKey) []cipher.PubKey {
	out := pks[:0:0]
	for _, p := range pks {
		if p != pk {
			out = append(out, p)
		}
	}
	return out
}

// persistHypervisors writes want to the config so a runtime add/remove survives
// a restart. Best-effort: a non-file-backed config (wasm tab / STDIN) can't write
// — the runtime connection change already took effect, so a write error is only
// logged, never fatal.
func (v *Visor) persistHypervisors(want []cipher.PubKey) {
	if v.conf == nil || v.conf.Path() == "" {
		return // not file-backed; runtime change stands without persistence
	}
	if err := v.conf.UpdateHypervisors(want); err != nil {
		v.log.WithError(err).Warn("failed to persist hypervisors to config")
	}
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
	if ok {
		// Tear down the live connection; its goroutine's deferred cleanup removes
		// hvPK from connectedHypervisors + hypervisorCancels. Config-loaded
		// hypervisors are tracked here too (initHypervisors registers them), so
		// this now works for them, not only AddHypervisor-added ones.
		cancel()
	}
	// Persist the removal regardless (even if the connection was already gone or
	// never tracked) so the visor doesn't reconnect to it on the next restart.
	v.persistHypervisors(removePK(v.configuredHypervisors(), hvPK))
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
	// Persist an empty configured set so none reconnect on restart (now that
	// config-loaded hypervisors are tracked + canceled here too).
	v.persistHypervisors(nil)
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

// SetHypervisorPasswordForce sets the hypervisor UI's "admin" account
// password WITHOUT the old one, creating the account if none exists. For
// the local privileged RPC path (skywire cli visor hv passwd --force):
// forgotten-password reset, or a first-time set from the CLI.
// Returns an error when this visor isn't hosting a hypervisor.
func (v *Visor) SetHypervisorPasswordForce(newPassword string) error {
	if v.hvInstance == nil {
		return fmt.Errorf("hypervisor not running on this visor")
	}
	return v.hvInstance.users.SetAdminPassword(newPassword)
}
