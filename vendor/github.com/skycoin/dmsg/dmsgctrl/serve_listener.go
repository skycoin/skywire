package dmsgctrl

import "net"

// ServeListener serves a listener with dmsgctrl.Control.
// It returns a channel for incoming Controls.
func ServeListener(l net.Listener, chanLen int) <-chan *Control {
	ch := make(chan *Control, chanLen)

	go func() {
		defer close(ch)

		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			if ctrl := ControlStream(conn); ch != nil && len(ch) < cap(ch) {
				ch <- ctrl
			}
		}
	}()

	return ch
}
