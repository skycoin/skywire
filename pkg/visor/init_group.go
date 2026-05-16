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
	"time"

	skychatgroup "github.com/skycoin/skywire/cmd/apps/skychat/group"
	"github.com/skycoin/skywire/cmd/apps/skychat/history"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// groupState holds the visor-side runtime state for chat groups.
// Lives on the Visor struct so RPC handlers can reach it.
type groupState struct {
	store   *skychatgroup.Store
	manager *skychatgroup.Manager
	inbox   *groupInbox
	// history is non-nil when group-message persistence is enabled.
	// The visor's GroupHistory RPC reads from this; the inbox's
	// deliver fans messages to it on the write side. nil when
	// persistence is off (the default).
	history GroupHistoryFetcher
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
		// Owner-side heartbeat emission. Every group this visor owns
		// publishes a no-op probe every interval so members can detect
		// a silently-stalled CXO subscriber via "no heartbeat in N
		// seconds" rather than "no traffic in 5min". The recommended
		// 30s cadence pairs with the member-side 90s stale threshold
		// for a 2-misses-then-reconnect policy.
		HeartbeatInterval: skychatgroup.DefaultHeartbeatInterval,
	})
	if err != nil {
		_ = store.Close() //nolint:errcheck
		log.WithError(err).Warn("Grouping: NewManager failed; group chat disabled this run")
		return nil
	}

	inbox := newGroupInbox(groupInboxCap)
	inbox.setManager(mgr)

	// Group history persistence — opt-in via Skychat.GroupHistoryDB
	// (when non-empty). Defaults to disabled: groups are usually
	// ephemeral within a session, and the in-memory ring (groupInboxCap
	// = 1024) is enough for the live-message use case. Operators who
	// want cross-restart recall set the DB path explicitly.
	var histFetcher GroupHistoryFetcher
	if v.conf.Skychat != nil && v.conf.Skychat.GroupHistoryDB != "" {
		dbPath := v.conf.Skychat.GroupHistoryDB
		// Use the same limits the chat-app's 1:1 store uses by
		// default. Operators can tune via a future config knob —
		// for now defaults are a sensible starting point that
		// guards against disk-fill from a noisy group.
		limits := history.DefaultLimits()
		// Group senders are already gated by group membership; the
		// per-key rate limit defeats burst-fanout (many members
		// sending to one group at once). Loosen it materially.
		limits.PerPeerRatePerMin = 0
		// Group caps want to be larger than 1:1 because of fan-in.
		// Keep TotalCapBytes from DefaultLimits as the global guard.
		limits.PerPeerCap = 5000

		histPath := dbPath
		if !filepath.IsAbs(histPath) {
			histPath = filepath.Join(dataDir, histPath)
		}
		hStore, err := history.NewBoltStore(histPath, limits)
		if err != nil {
			log.WithError(err).Warn("Grouping: history store open failed; persistence disabled this run")
		} else {
			adapter := &groupHistoryAdapter{store: hStore, log: log}
			histFetcher = adapter
			inbox.hist = adapter
			log.WithField("db", histPath).Info("Grouping: history persistence enabled")
			v.pushCloseStack("grouping.history", func() error {
				return hStore.Close()
			})
		}
	}

	mgr.SetMessageHandler(inbox.deliver)

	v.initLock.Lock()
	v.grouping.store = store
	v.grouping.manager = mgr
	v.grouping.inbox = inbox
	v.grouping.history = histFetcher
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

// groupHistoryAdapter bridges the visor-side groupHistorySink (write)
// and GroupHistoryFetcher (read) interfaces to the concrete
// history.BoltStore implementation. The two interfaces live in
// pkg/visor/group.go to keep pkg/visor decoupled at the type level
// from cmd/apps/skychat/history — this adapter is the only spot
// that needs both sides of that boundary.
//
// Append errors are logged-then-swallowed at the write call site
// (inbox.deliver) so persistence is best-effort and never blocks
// live delivery. Read errors propagate to RPC callers.
type groupHistoryAdapter struct {
	store *history.BoltStore
	log   *logging.Logger
}

// AppendGroup implements groupHistorySink (write path).
func (a *groupHistoryAdapter) AppendGroup(groupID, senderPK, text string, ts time.Time, outgoing bool) error {
	err := a.store.AppendGroup(history.GroupMessage{
		GroupID:   groupID,
		SenderPK:  senderPK,
		Text:      text,
		Timestamp: ts,
		Outgoing:  outgoing,
	})
	if err != nil {
		// Log at debug — most "errors" are expected (rate limit, cap,
		// whitelist). Only WARN on backend storage errors that suggest
		// the disk is misbehaving.
		switch err {
		case history.ErrRateLimited, history.ErrTooLarge, history.ErrStorageFull,
			history.ErrNotWhitelisted, history.ErrEmptyPeer:
			a.log.WithError(err).WithField("group", groupID).Debug("grouping: history skip")
		default:
			a.log.WithError(err).WithField("group", groupID).Warn("grouping: history append failed")
		}
	}
	return err
}

// ListByGroup implements GroupHistoryFetcher (read path). Converts
// history.GroupMessage → visor.GroupMessage so RPC callers see the
// existing on-the-wire shape (matching GroupPoll's return type).
func (a *groupHistoryAdapter) ListByGroup(groupID string, limit int) ([]GroupMessage, error) {
	hMsgs, err := a.store.ListByGroup(groupID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]GroupMessage, 0, len(hMsgs))
	for _, m := range hMsgs {
		var senderPK cipher.PubKey
		if err := senderPK.Set(m.SenderPK); err != nil {
			// Skip any record with an invalid PK on disk rather than
			// failing the whole query. Should never happen if the
			// write path always validates, but defends against schema
			// drift.
			a.log.WithError(err).WithField("group", groupID).Debug("grouping: history skip bad pk")
			continue
		}
		out = append(out, GroupMessage{
			GroupID:  m.GroupID,
			SenderPK: senderPK,
			Text:     m.Text,
			TS:       m.Timestamp,
		})
	}
	return out, nil
}

// Groups implements GroupHistoryFetcher.
func (a *groupHistoryAdapter) Groups() ([]string, error) {
	return a.store.Groups()
}

// ListGroupSince implements groupHistorySink (read path beyond the
// inbox ring). Walks the bolt store from the first message with TS
// strictly after `since`, returning visor-shaped GroupMessage values
// in chronological order. The bolt key is the message timestamp so
// the underlying call is a cursor Seek + forward walk — bounded by
// "messages since the cursor" rather than "all messages in the
// group" even on long-running groups.
//
// Invalid-PK records on disk are skipped with a debug log (same
// defense-in-depth as ListByGroup); the rest of the result stream
// is preserved.
func (a *groupHistoryAdapter) ListGroupSince(groupID string, since time.Time) ([]GroupMessage, error) {
	hMsgs, err := a.store.ListGroupSince(groupID, since)
	if err != nil {
		return nil, err
	}
	out := make([]GroupMessage, 0, len(hMsgs))
	for _, m := range hMsgs {
		var senderPK cipher.PubKey
		if err := senderPK.Set(m.SenderPK); err != nil {
			a.log.WithError(err).WithField("group", groupID).Debug("grouping: history-since skip bad pk")
			continue
		}
		out = append(out, GroupMessage{
			GroupID:  m.GroupID,
			SenderPK: senderPK,
			Text:     m.Text,
			TS:       m.Timestamp,
		})
	}
	return out, nil
}
