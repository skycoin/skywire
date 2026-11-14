package group

import (
	"net"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skychat/message"
)

// TestRelayDropsSilentStream opens a relay stream and sends nothing. The
// handler must give up after the first frame timeout instead of holding it.
func TestRelayDropsSilentStream(t *testing.T) {
	old := message.FirstFrameTimeout
	message.FirstFrameTimeout = 100 * time.Millisecond
	t.Cleanup(func() { message.FirstFrameTimeout = old })

	s := &Session{log: logging.MustGetLogger("group.relay-silent-test")}
	member, admin := net.Pipe()
	defer member.Close() //nolint:errcheck
	defer admin.Close()  //nolint:errcheck
	pk, _ := cipher.GenerateKeyPair()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleRelay(fakeAddrConn{Conn: admin, addr: dmsg.Addr{PK: pk, Port: 1}})
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handleRelay still waits on a silent stream")
	}
}
