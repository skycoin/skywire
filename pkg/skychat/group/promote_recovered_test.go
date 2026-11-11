// Package group pkg/skychat/group/promote_recovered_test.go c4-app-chat
package group

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// A group left pending by a failed first connect becomes active once its
// subscriber is receiving again, even when the subscriber's own watchdog did
// the reconnecting.
func TestDetectStale_PromotesPendingWithFreshSubscriber(t *testing.T) {
	me, _ := cipher.GenerateKeyPair()
	owner, _ := cipher.GenerateKeyPair()
	m := newStateTestManager(t, me)
	const id = "9861f2b2-6c70-4de9-a97b-f72236957d56"
	require.NoError(t, m.store.Put(Record{ID: id, OwnerPK: owner, Role: RoleMember, Status: StatusPending}))

	sess := &Session{}
	sess.lastInboundNs.Store(time.Now().UnixNano())
	installSession(m, id, sess)

	m.detectStaleAndReconnect(context.Background())

	got, ok, err := m.store.Get(id)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, StatusActive, got.Status)
}
