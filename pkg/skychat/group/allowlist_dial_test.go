package group

import (
	"net"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// TestSetAllowlist_UnreachableMemberDoesNotBlock adds a member whose CXO
// address accepts and then never answers. SetAllowlist and the peerSubsMu
// readers behind group info and the reconnect loop must not wait for it.
func TestSetAllowlist_UnreachableMemberDoesNotBlock(t *testing.T) {
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = silent.Close() }) //nolint:errcheck
	var held []net.Conn
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			held = append(held, c)
		}
	}()

	ownerPK, ownerSK := cipher.GenerateKeyPair()
	newPK, _ := cipher.GenerateKeyPair()
	s, err := Open(Config{
		MyPK: ownerPK, MySK: ownerSK,
		Record: Record{
			ID: uuid.NewString(), Name: "dial", OwnerPK: ownerPK, Port: 8870,
			Mode: ModePublic, Role: RoleOwner, Members: []cipher.PubKey{ownerPK},
		},
		TCPListenAddr: freeAddr(t),
		PeerAddrs:     map[cipher.PubKey]string{newPK: silent.Addr().String()},
		InMemoryDB:    true,
		BatchWindow:   20 * time.Millisecond,
		Logger:        logging.MustGetLogger("group-dial-test"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() }) //nolint:errcheck

	start := time.Now()
	if _, err := s.SetAllowlist([]cipher.PubKey{ownerPK, newPK}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("SetAllowlist took %s with an unreachable member", d)
	}

	read := make(chan []cipher.PubKey, 1)
	go func() { read <- s.PeerPKs() }()
	select {
	case pks := <-read:
		if len(pks) != 1 || pks[0] != newPK {
			t.Fatalf("PeerPKs = %v, want [%s]", pks, newPK)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PeerPKs blocked behind the dial to an unreachable member")
	}

	start = time.Now()
	if _, err := s.SetAdminRoster([]cipher.PubKey{ownerPK}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("SetAdminRoster took %s", d)
	}
}
