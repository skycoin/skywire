// Package appevent pkg/app/appevent/utils.go
package appevent

import (
	"context"
)

// SendTCPDial sends tcp dial event
func (eb *Broadcaster) SendTCPDial(ctx context.Context, remoteNet, remoteAddr string) { //nolint:all
	data := TCPDialData{RemoteNet: remoteNet, RemoteAddr: remoteAddr}
	event := NewEvent(TCPDial, data)
	eb.sendEvent(ctx, event)
}

// SendTPClose sends transport close event
func (eb *Broadcaster) SendTPClose(ctx context.Context, netType, addr string) { //nolint:all
	data := TCPCloseData{RemoteNet: string(netType), RemoteAddr: addr}
	event := NewEvent(TCPClose, data)
	if err := eb.Broadcast(context.Background(), event); err != nil {
		eb.log.WithError(err).Errorln("Failed to broadcast TCPClose event")
	}
}

func (eb *Broadcaster) sendEvent(ctx context.Context, event *Event) { //nolint:all
	err := eb.Broadcast(context.Background(), event) //nolint:errcheck
	if err != nil {
		eb.log.Warn("Failed to broadcast event: %v", event)
	}
}
