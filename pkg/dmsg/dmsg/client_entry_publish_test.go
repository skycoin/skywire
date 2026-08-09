// Package dmsg pkg/dmsg/dmsg/client_entry_publish_test.go c1-net-dmsg
package dmsg

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// selfEntryDisc answers Entry() for the client's own PK with an entry that
// ALREADY lists delegated servers, and counts the writes it receives.
//
// This is the shape a visor actually sees: its disc client is a registering
// fallback whose reads resolve direct-first, and the visor seeds its own PK
// into that direct client, where direct.GetAllEntries synthesizes a self-entry
// listing every configured server as delegated. The read therefore describes
// local config, not what dmsg-discovery holds.
type selfEntryDisc struct {
	disc.APIClient // unused methods panic if ever called

	mx        sync.Mutex
	selfEntry *disc.Entry
	puts      int
	posts     int
}

func (d *selfEntryDisc) Entry(_ context.Context, _ cipher.PubKey) (*disc.Entry, error) {
	d.mx.Lock()
	defer d.mx.Unlock()
	return d.selfEntry, nil
}

func (d *selfEntryDisc) PutEntry(_ context.Context, _ cipher.SecKey, _ *disc.Entry) error {
	d.mx.Lock()
	defer d.mx.Unlock()
	d.puts++
	return nil
}

func (d *selfEntryDisc) PostEntry(_ context.Context, _ *disc.Entry) error {
	d.mx.Lock()
	defer d.mx.Unlock()
	d.posts++
	return nil
}

func (d *selfEntryDisc) writes() (posts, puts int) {
	d.mx.Lock()
	defer d.mx.Unlock()
	return d.posts, d.puts
}

// TestUpdateClientEntry_FirstPublishIsNotSkipped is the regression guard for a
// fleet-wide registration stall: a client whose discovery READ already reports
// the delegated servers it was about to publish must still publish, once,
// because the read is not proof that the discovery ever received it.
//
// The failure it pins: with a single dmsg server, the synthetic self-entry the
// direct client serves lists exactly the one server the live session is on, so
// the read-modify-write found "no delta" on the very first publish and returned
// without writing. The client then stayed absent from dmsg-discovery until the
// periodic tick (5 min for clients) — long enough that every e2e test gated on
// "visor appears in DMSG discovery" timed out, and long enough in production
// that a rebooted visor was undialable for minutes.
func TestUpdateClientEntry_FirstPublishIsNotSkipped(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	srvPK, _ := cipher.GenerateKeyPair()

	dc := &selfEntryDisc{
		// The delegated set the client is about to publish — already present.
		selfEntry: disc.NewClientEntry(pk, 1, []cipher.PubKey{srvPK}),
	}

	c := new(EntityCommon)
	c.init(pk, sk, dc, logging.MustGetLogger("test"), time.Minute*5)
	c.sessions[srvPK] = &SessionCommon{rPK: srvPK}

	// initilizeClientEntry records an update before any session exists, so the
	// periodic timer is NOT due when the first session lands. Without that the
	// due-timer alone would force the write and hide the bug.
	c.recordUpdate()
	_, due := c.updateIsDue()
	require.False(t, due, "test setup: the first publish must race a not-due timer")

	done := make(chan struct{})
	require.NoError(t, c.updateClientEntry(context.Background(), done, "visor"))

	_, puts := dc.writes()
	require.Equal(t, 1, puts, "first publish must reach the discovery even when the read shows no delta")

	// Steady state is unchanged: same server set, still not due, no second write.
	// This is the guard that keeps reconnect storms off the discovery.
	require.NoError(t, c.updateClientEntry(context.Background(), done, "visor"))
	_, puts = dc.writes()
	require.Equal(t, 1, puts, "an unchanged server set must not republish before the entry is due")

	// A change in the delegated set publishes again immediately.
	srv2PK, _ := cipher.GenerateKeyPair()
	c.sessions[srv2PK] = &SessionCommon{rPK: srv2PK}
	require.NoError(t, c.updateClientEntry(context.Background(), done, "visor"))
	_, puts = dc.writes()
	require.Equal(t, 2, puts, "a changed server set must republish")
}

// TestUpdateClientEntry_NoSessionsDoesNotForcePublish keeps the first-publish
// override narrow: it is for a client that has a delegated set to announce, not
// for one whose update loop ticks before any session exists. Publishing an
// empty delegated set would advertise an unreachable client.
func TestUpdateClientEntry_NoSessionsDoesNotForcePublish(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()

	dc := &selfEntryDisc{selfEntry: disc.NewClientEntry(pk, 1, nil)}

	c := new(EntityCommon)
	c.init(pk, sk, dc, logging.MustGetLogger("test"), time.Minute*5)
	c.recordUpdate()

	require.NoError(t, c.updateClientEntry(context.Background(), make(chan struct{}), "visor"))
	posts, puts := dc.writes()
	require.Zero(t, puts)
	require.Zero(t, posts)
}
