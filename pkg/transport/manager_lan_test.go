// Package transport — pkg/transport/manager_lan_test.go: white-box unit
// tests for the network-topology helpers and the transport-list snapshot
// paths of the Manager that need no live networking: the LAN/hairpin
// classifiers (isSameLANEndpoint / sameIPv4Slash24 / hostIsIP), the
// TransportRemoteAddrs / SameLANPeers surface, currentBareEntries'
// closed/self-loop filtering, the CXO transport-list publisher
// (publishTPDList, compact form), and the local delete paths.
package transport

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func TestHostIsIP(t *testing.T) {
	require.True(t, hostIsIP("1.2.3.4"))
	require.True(t, hostIsIP("1.2.3.4:5000"))
	require.True(t, hostIsIP("[2001:db8::1]:443"))
	require.False(t, hostIsIP("example.com:80"))
	require.False(t, hostIsIP("webrtc-datachannel(pk)"))
	require.False(t, hostIsIP(""))
}

func TestSameIPv4Slash24(t *testing.T) {
	require.True(t, sameIPv4Slash24(net.ParseIP("1.2.3.4"), net.ParseIP("1.2.3.9")))
	require.False(t, sameIPv4Slash24(net.ParseIP("1.2.3.4"), net.ParseIP("1.2.4.4")))
	// An IPv6 address has no /24 comparison.
	require.False(t, sameIPv4Slash24(net.ParseIP("2001:db8::1"), net.ParseIP("2001:db8::2")))
	require.False(t, sameIPv4Slash24(net.ParseIP("1.2.3.4"), net.ParseIP("2001:db8::2")))
}

func TestIsSameLANEndpoint(t *testing.T) {
	self := net.ParseIP("8.8.8.8")
	locals := []net.IP{net.ParseIP("203.0.113.5")}

	// nil peer is never LAN.
	require.False(t, isSameLANEndpoint(nil, self, locals))
	// A private / RFC1918 endpoint is reachable only over the LAN.
	require.True(t, isSameLANEndpoint(net.ParseIP("192.168.1.20"), self, locals))
	require.True(t, isSameLANEndpoint(net.ParseIP("10.0.0.7"), nil, nil))
	// NAT hairpin: peer reached at our own public IP (exact + /24).
	require.True(t, isSameLANEndpoint(net.ParseIP("8.8.8.8"), self, nil))
	require.True(t, isSameLANEndpoint(net.ParseIP("8.8.8.20"), self, nil))
	// Public peer sharing a local interface /24.
	require.True(t, isSameLANEndpoint(net.ParseIP("203.0.113.99"), self, locals))
	// Unrelated public peer is not LAN.
	require.False(t, isSameLANEndpoint(net.ParseIP("1.1.1.1"), self, locals))
	// Public peer, self not yet known (STUN pending) and no matching local.
	require.False(t, isSameLANEndpoint(net.ParseIP("1.1.1.1"), nil, nil))
}

func TestManagerTransportRemoteAddrs(t *testing.T) {
	tm := newTestManager(t)

	// A non-DMSG transport with a routable IP endpoint is emitted.
	remote := mustPK(t)
	servingTransport(t, tm, remote, types.STCPR)

	// A DMSG transport is always skipped (no routable raw IP).
	servingTransport(t, tm, mustPK(t), types.DMSG)

	addrs := tm.TransportRemoteAddrs()
	require.Equal(t, []string{"1.2.3.4:5000"}, addrs)
}

func TestManagerSameLANPeers(t *testing.T) {
	tm := newTestManager(t)
	remote := mustPK(t)
	servingTransport(t, tm, remote, types.STCPR) // memTransport endpoint is 1.2.3.4:5000

	// Hairpin: our own public IP equals the peer endpoint IP → same LAN.
	peers := tm.SameLANPeers(net.ParseIP("1.2.3.4"))
	require.Equal(t, []cipher.PubKey{remote}, peers)

	// A DMSG transport is never considered a LAN peer.
	tm2 := newTestManager(t)
	servingTransport(t, tm2, mustPK(t), types.DMSG)
	require.Empty(t, tm2.SameLANPeers(net.ParseIP("1.2.3.4")))
}

func TestManagerCurrentBareEntries(t *testing.T) {
	tm := newTestManager(t)

	// Live, ordinary transport → included.
	live, _ := servingTransport(t, tm, mustPK(t), types.STCPR)

	// Closed transport → excluded even though still in tm.tps.
	closed, _ := servingTransport(t, tm, mustPK(t), types.STCPR)
	closed.CloseForTest() // mark closed without the retrying discovery deregister

	// Self-loop transport (both edges the same PK) → excluded.
	selfMT := NewManagedTransportForTest(newMemTransport())
	selfMT.Entry = MakeEntry(tm.Conf.PubKey, tm.Conf.PubKey, types.STCPR, LabelUser)
	tm.tps[selfMT.Entry.ID] = selfMT

	entries := tm.currentBareEntries()
	require.Len(t, entries, 1)
	require.Equal(t, live.Entry.ID, entries[0].ID)
}

// captureLeafPub records the last Put/Delete so publishTPDList's compact
// snapshot can be inspected.
type captureLeafPub struct {
	path    string
	body    []byte
	deletes []string
}

func (c *captureLeafPub) Put(path string, value []byte) error {
	c.path, c.body = path, value
	return nil
}
func (c *captureLeafPub) Delete(path string) error {
	c.deletes = append(c.deletes, path)
	return nil
}

func TestManagerPublishTPDList(t *testing.T) {
	tm := newTestManager(t)
	self := tm.Conf.PubKey
	remote := mustPK(t)
	entry := MakeEntry(self, remote, types.STCPR, LabelSkycoin)

	// No publisher installed → no-op (must not panic).
	tm.publishTPDList([]*Entry{&entry})

	cap := &captureLeafPub{}
	tm.SetTPDLeafPublisher(cap)

	tm.publishTPDList([]*Entry{&entry})
	require.Equal(t, tpdListPath, cap.path)

	var got struct {
		Version string         `json:"version,omitempty"`
		Compact []CompactEntry `json:"c"`
	}
	require.NoError(t, json.Unmarshal(cap.body, &got))
	require.Len(t, got.Compact, 1)
	// Compact form drops the reporter's own PK and keeps the remote edge + type.
	require.Equal(t, remote, got.Compact[0].Remote)
	require.Equal(t, types.STCPR, got.Compact[0].Type)
	require.Equal(t, LabelSkycoin, got.Compact[0].Label)

	// nil entries publishes an explicit empty list.
	tm.publishTPDList(nil)
	require.NoError(t, json.Unmarshal(cap.body, &got))
	require.Empty(t, got.Compact)
}

// registerInOwnDisc records mt.Entry in the transport's own discovery mock so
// the close()/deleteFromDiscovery path succeeds on the first try instead of
// retrying against an empty mock (which backs off for ~30s).
func registerInOwnDisc(t *testing.T, mt *ManagedTransport) {
	t.Helper()
	require.NoError(t, mt.dc.RegisterTransportsV3(context.Background(), "v3", &mt.Entry))
}

func TestManagerDeleteTransport(t *testing.T) {
	tm := newTestManager(t)
	mt, _ := servingTransport(t, tm, mustPK(t), types.STCPR)
	registerInOwnDisc(t, mt)
	id := mt.Entry.ID
	require.Equal(t, 1, tm.TransportCount())

	// Deleting an unknown id is a safe no-op.
	tm.DeleteTransport(uuid.New())
	require.Equal(t, 1, tm.TransportCount())

	tm.DeleteTransport(id)
	require.Equal(t, 0, tm.TransportCount())
}

func TestManagerDeleteAllTransports(t *testing.T) {
	tm := newTestManager(t)
	mt1, _ := servingTransport(t, tm, mustPK(t), types.STCPR)
	mt2, _ := servingTransport(t, tm, mustPK(t), types.SUDPH)
	registerInOwnDisc(t, mt1)
	registerInOwnDisc(t, mt2)
	require.Equal(t, 2, tm.TransportCount())

	tm.DeleteAllTransports()
	require.Equal(t, 0, tm.TransportCount())
}

func TestManagerTpIDFromPK(t *testing.T) {
	tm := newTestManager(t)
	remote := mustPK(t)
	require.Equal(t,
		MakeTransportID(tm.Conf.PubKey, remote, types.STCPR),
		tm.tpIDFromPK(remote, types.STCPR))
}
