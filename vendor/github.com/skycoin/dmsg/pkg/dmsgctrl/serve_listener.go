// Package dmsgctrl pkg/dmsgctrl/serve_listener.go
package dmsgctrl

import (
	"errors"
	"net"
	"strings"

	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

var log = logging.MustGetLogger("dmsgctrl")

// ServeListener serves a listener with dmsgctrl.Control.
// It returns a channel for incoming Controls.
func ServeListener(l net.Listener, chanLen int) <-chan *Control {
	ch := make(chan *Control, chanLen)

	go func() {
		defer close(ch)

		for {
			conn, err := l.Accept()
			if err != nil {
				if errors.Is(err, dmsg.ErrEntityClosed) || strings.Contains(err.Error(), "use of closed") {
					return
				}
				log.Warnf("Failed to accept dmsgctrl conn, continuing: %v", err)
				continue
			}
			ctrl := ControlStream(conn)
			select {
			case ch <- ctrl:
			default:
				log.Warnf("Control channel full, dropping and closing control")
				if err := ctrl.Close(); err != nil {
					log.Warnf("Failed to close dropped control: %v", err)
				}
			}
		}
	}()

	return ch
}
