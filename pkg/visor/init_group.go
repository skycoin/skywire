// Package visor pkg/visor/init_group.go
//
// Init wiring for the chat-group feed manager. Parallel to
// init_pairing.go — same shape, same best-effort failure
// semantics, same close-stack registration. Depends on dmsgC, opens
// its own bbolt store under v.conf.LocalPath/skychat/groups.db, and
// resumes any non-terminal group records persisted from a previous
// run.
package visor

import (
	"context"
	"path/filepath"

	skychatgroup "github.com/skycoin/skywire/cmd/apps/skychat/group"
	"github.com/skycoin/skywire/pkg/logging"
)

// groupState holds the visor-side runtime state for chat groups.
// Lives on the Visor struct so RPC handlers can reach it.
type groupState struct {
	store   *skychatgroup.Store
	manager *skychatgroup.Manager
	inbox   *groupInbox
}

// groupInboxCap mirrors defaultInboxCap on the pairing side. A few
// hundred messages a minute under burst, hard cap before unbounded
// growth if the consumer is slow or absent.
const groupInboxCap = 1024

// initGrouping brings up the per-visor chat-group feed manager. Best
// effort: a failure here logs and disables group chat for this run
// rather than failing visor startup. Group RPC handlers return
// ErrGroupingDisabled when the manager is absent.
func initGrouping(_ context.Context, v *Visor, log *logging.Logger) error {
	if v.dmsgC == nil {
		log.Debug("Grouping: dmsg client absent; manager not started")
		return nil
	}
	dataDir := filepath.Join(v.conf.LocalPath, "skychat")
	storePath := filepath.Join(dataDir, "groups.db")

	store, err := skychatgroup.OpenStore(storePath)
	if err != nil {
		log.WithError(err).Warn("Grouping: open store failed; group chat disabled this run")
		return nil
	}

	mgr, err := skychatgroup.NewManager(skychatgroup.ManagerConfig{
		Store:   store,
		DmsgC:   v.dmsgC,
		MyPK:    v.conf.PK,
		MySK:    v.conf.SK,
		DataDir: filepath.Join(dataDir, "cxo-groups"),
		Logger:  log,
	})
	if err != nil {
		_ = store.Close() //nolint:errcheck
		log.WithError(err).Warn("Grouping: NewManager failed; group chat disabled this run")
		return nil
	}

	inbox := newGroupInbox(groupInboxCap)
	inbox.setManager(mgr)
	mgr.SetMessageHandler(inbox.deliver)

	v.initLock.Lock()
	v.grouping.store = store
	v.grouping.manager = mgr
	v.grouping.inbox = inbox
	v.initLock.Unlock()

	if err := mgr.Resume(); err != nil {
		log.WithError(err).Warn("Grouping: Resume returned error; partial recovery only")
	}
	log.Info("Grouping: manager ready")

	v.pushCloseStack("grouping", func() error {
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
