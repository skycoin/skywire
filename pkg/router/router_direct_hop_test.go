package router

import (
	"testing"

	"github.com/google/uuid"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

// injectHopTp adds a live transport between the router's own key and remote.
func injectHopTp(t *testing.T, r *router, remote cipher.PubKey, label transport.Label) uuid.UUID {
	t.Helper()
	id := uuid.New()
	mt := transport.NewManagedTransportForTest(newWorkingTransport())
	mt.Entry = transport.Entry{
		ID:    id,
		Type:  "test",
		Edges: [2]cipher.PubKey{r.conf.PubKey, remote},
		Label: label,
	}
	r.tm.InjectTransportForTest(mt)
	return id
}

// TestDirectHopFindsExistingTransport is the point of #4552: a --direct dial
// over a transport this visor already holds must not need the route finder,
// which would only hand back the very transport being held.
func TestDirectHopFindsExistingTransport(t *testing.T) {
	r := newLegTestRouter(t)
	remote, _ := cipher.GenerateKeyPair()
	id := injectHopTp(t, r, remote, transport.LabelUser)

	hop, ok := r.directHop(r.conf.PubKey, remote)
	if !ok {
		t.Fatal("directHop found no route over an existing transport")
	}
	if hop.TpID != id {
		t.Errorf("hop uses transport %v, want %v", hop.TpID, id)
	}
	if hop.From != r.conf.PubKey || hop.To != remote {
		t.Errorf("hop is %v -> %v, want %v -> %v", hop.From, hop.To, r.conf.PubKey, remote)
	}
}

// TestDirectHopSkipsSetupTransport guards the one transport that must never
// become an app-data route: setup transports carry route-setup traffic.
func TestDirectHopSkipsSetupTransport(t *testing.T) {
	r := newLegTestRouter(t)
	remote, _ := cipher.GenerateKeyPair()
	injectHopTp(t, r, remote, transport.LabelSetup)

	if _, ok := r.directHop(r.conf.PubKey, remote); ok {
		t.Fatal("directHop routed over a setup transport")
	}
}

// TestDirectHopNoTransportFallsThrough pins the fall-through: with no transport
// to dst, directHop must report no route so the caller uses the route finder
// rather than failing the dial.
func TestDirectHopNoTransportFallsThrough(t *testing.T) {
	r := newLegTestRouter(t)
	other, _ := cipher.GenerateKeyPair()
	unrelated, _ := cipher.GenerateKeyPair()
	injectHopTp(t, r, other, transport.LabelUser)

	if _, ok := r.directHop(r.conf.PubKey, unrelated); ok {
		t.Fatal("directHop invented a route to a peer it has no transport to")
	}
}
