// Package visor pkg/visor/init_pairing.go
//
// Init wiring for the chat-pair feed manager. Mirrors initStats and
// initCXOUserFeeds: depends on dmsgC, opens its own bbolt store under
// v.conf.LocalPath/pairing/, and resumes any pairs persisted from a
// previous run.
//
// The pairing module sits intentionally low in the dependency graph
// — it doesn't need transports, routes, or the launcher — so the
// only init-ordering constraint is "after dmsgC."
package visor

import (
	"context"
	"path/filepath"

	"github.com/skycoin/skywire/cmd/apps/skychat/pairing"
	"github.com/skycoin/skywire/pkg/logging"
)

// pairingState holds the visor-side runtime state for chat pairs.
// Lives on the Visor struct so RPC handlers can reach it; populated
// by initPairing during module startup.
type pairingState struct {
	store   *pairing.Store
	manager *pairing.Manager
	inbox   *pairInbox
}

// initPairing brings up the per-visor chat-pair feed manager. Best-
// effort: a failure here logs and disables pairing for this run
// rather than failing visor startup. Pairing is opt-in (pairs are
// only created via PairAdd RPC) so a missing manager just yields
// "no pairs" — no other subsystem depends on it.
func initPairing(_ context.Context, v *Visor, log *logging.Logger) error {
	if v.dmsgC == nil {
		log.Debug("Pairing: dmsg client absent; manager not started")
		return nil
	}
	dataDir := filepath.Join(v.conf.LocalPath, "pairing")
	storePath := filepath.Join(dataDir, "pairs.db")

	store, err := pairing.OpenStore(storePath)
	if err != nil {
		log.WithError(err).Warn("Pairing: open store failed; pairing disabled this run")
		return nil
	}

	mgr, err := pairing.NewManager(pairing.ManagerConfig{
		Store:   store,
		DmsgC:   v.dmsgC,
		MyPK:    v.conf.PK,
		MySK:    v.conf.SK,
		DataDir: filepath.Join(dataDir, "cxo"),
		Logger:  log,
	})
	if err != nil {
		_ = store.Close() //nolint:errcheck
		log.WithError(err).Warn("Pairing: NewManager failed; pairing disabled this run")
		return nil
	}

	inbox := newPairInbox(defaultInboxCap)
	mgr.SetMessageHandler(inbox.deliver)

	v.initLock.Lock()
	v.pairing.store = store
	v.pairing.manager = mgr
	v.pairing.inbox = inbox
	v.initLock.Unlock()

	resumed, err := mgr.Resume()
	if err != nil {
		log.WithError(err).Warn("Pairing: Resume returned error; partial recovery only")
	}
	log.WithField("resumed", resumed).Info("Pairing: manager ready")

	v.pushCloseStack("pairing", func() error {
		var firstErr error
		if mgr != nil {
			if err := mgr.Close(); err != nil {
				firstErr = err
			}
		}
		if store != nil {
			if err := store.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	})

	return nil
}
