package appevent

import "context"

// SendTCPDial sends tcp dial event
func (eb *Broadcaster) SendTCPDial(ctx context.Context, remoteNet, remoteAddr string) {
	data := TCPDialData{RemoteNet: remoteNet, RemoteAddr: remoteAddr}
	event := NewEvent(TCPDial, data)
	eb.sendEvent(ctx, event)
}

func (eb *Broadcaster) sendEvent(_ context.Context, event *Event) {
	err := eb.Broadcast(context.Background(), event) //nolint:errcheck
	if err != nil {
		eb.log.Warn("Failed to broadcast event: %v", event)
	}
}
