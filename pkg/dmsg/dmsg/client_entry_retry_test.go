// Package dmsg pkg/dmsg/dmsg/client_entry_retry_test.go c1-net-dmsg
package dmsg

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// flakyPutDisc fails the first failPuts PutEntry calls and succeeds after,
// counting every attempt. Reads always answer, so the publish gets as far as
// the write.
type flakyPutDisc struct {
	disc.APIClient // unused methods panic if ever called

	mx       sync.Mutex
	entry    *disc.Entry
	failPuts int
	attempts int
}

func (d *flakyPutDisc) Entry(_ context.Context, _ cipher.PubKey) (*disc.Entry, error) {
	d.mx.Lock()
	defer d.mx.Unlock()
	return d.entry, nil
}

func (d *flakyPutDisc) PutEntry(_ context.Context, _ cipher.SecKey, _ *disc.Entry) error {
	d.mx.Lock()
	defer d.mx.Unlock()
	d.attempts++
	if d.attempts <= d.failPuts {
		return errors.New("dmsg error 202 - cannot connect to delegated server")
	}
	return nil
}

func (d *flakyPutDisc) attemptCount() int {
	d.mx.Lock()
	defer d.mx.Unlock()
	return d.attempts
}

// TestUpdateClientEntryLoop_RetriesAfterFailedPublish pins the retry ladder in
// updateClientEntryLoop against the due-gate that used to swallow it.
//
// The gate was added with the CXO keepalive stretch (#3829) and sits in front
// of the exponential backoff added by #3168. A failed publish does not advance
// lastUpdate, so when the 1 s backoff timer fired the entry was still "not
// due", the gate reset the timer to the full updateInterval and dropped the
// retry on the floor — silently, since that branch logs nothing.
//
// The consequence in the field: a client that lost the dmsg-HTTP cold-start
// race on its first publish stayed in discovery with an EMPTY delegated-server
// list, and so unresolvable by anyone looking it up, until the whole interval
// elapsed — 30 minutes once the CXO keepalive reports healthy. It is what put
// e2e's "visor did not appear in DMSG discovery with delegated servers within
// 2m0s" on the board, and it is the same wedge shape as #3157/#3168.
//
// updateInterval is an hour here, so the periodic tick can never fire: every
// attempt after the first is necessarily the backoff doing its job.
func TestUpdateClientEntryLoop_RetriesAfterFailedPublish(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	srvPK, _ := cipher.GenerateKeyPair()

	dc := &flakyPutDisc{
		entry:    disc.NewClientEntry(pk, 1, nil),
		failPuts: 2,
	}

	c := new(EntityCommon)
	c.init(pk, sk, dc, logging.MustGetLogger("test"), time.Hour)
	c.sessions[srvPK] = &SessionCommon{rPK: srvPK}

	// A publish landed at some earlier point, so the periodic timer is not
	// due. This is the steady state a session flap runs into.
	c.recordUpdate()
	_, due := c.updateIsDue()
	require.False(t, due, "test setup: the retry must race a not-due timer")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	defer close(done)

	go c.updateClientEntryLoop(ctx, done, "dmsg")

	// A session change nudges the loop into its first attempt, which fails.
	c.nudgeEntryUpdate()

	// Backoff is 1 s then 2 s, so the third attempt — the one that succeeds —
	// lands about 4 s in counting the 1 s debounce. Without the fix the count
	// stops at 1 and the loop sleeps for the full hour.
	require.Eventually(t, func() bool {
		return dc.attemptCount() >= 3
	}, 20*time.Second, 100*time.Millisecond,
		"loop stopped retrying after a failed publish; the due-gate swallowed the backoff")

	// And the successful attempt is what records the update.
	require.Eventually(t, func() bool {
		c.pushedSrvPKsMx.Lock()
		defer c.pushedSrvPKsMx.Unlock()
		return len(c.lastPushedSrvPKs) == 1
	}, 5*time.Second, 100*time.Millisecond, "successful publish did not record the delegated set")
}

// TestInitilizeClientEntryDoesNotRecordUpdate pins the other half: the initial
// type/protocol POST runs before any session exists, so the entry it writes
// carries no delegated servers at all. Stamping lastUpdate for it told the
// update loop the registration was current and pushed the first real publish a
// whole updateInterval away — the amplifier that turned one lost cold-start
// race into a 30-minute outage.
func TestInitilizeClientEntryDoesNotRecordUpdate(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()

	dc := &flakyPutDisc{entry: disc.NewClientEntry(pk, 1, nil)}

	c := new(EntityCommon)
	c.init(pk, sk, dc, logging.MustGetLogger("test"), time.Hour)

	require.NoError(t, c.initilizeClientEntry(context.Background(), "dmsg", "tcp"))

	_, due := c.updateIsDue()
	require.True(t, due, "the initial post published no delegated servers; it must not count as a registration")
}
