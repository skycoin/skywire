// Package api pkg/deployment/ar/api/udp_resolve.go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/ar/store"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

const udpResolveTimeout = 5 * time.Second

// resolveOverUDP answers a sudph lookup sent on requester's control
// connection, and asks the peer to dial back, as GET /resolve/sudph does.
// It returns an error only when the reply cannot be written.
func (a *API) resolveOverUDP(conn net.Conn, requester cipher.PubKey, req addrresolver.UDPResolveRequest) error {
	reply := addrresolver.UDPResolveReply{Resolved: req.ID}
	var target cipher.PubKey
	if err := target.Set(req.Resolve); err != nil {
		reply.Err = "bad public key"
		return a.writeUDPReply(conn, reply)
	}
	ctx, cancel := context.WithTimeout(context.Background(), udpResolveTimeout)
	defer cancel()
	data, err := a.store.Resolve(ctx, types.SUDPH, target)
	switch {
	case err == nil:
		reply.Found, reply.Data = true, &data
		a.countResolve(string(types.SUDPH), resolveFound)
	case errors.Is(err, store.ErrNoEntry), errors.Is(err, store.ErrUnknownTransportType):
		a.countResolve(string(types.SUDPH), resolveNotFound)
	default:
		reply.Err = err.Error()
	}
	a.counters.add(chartLookupUDP)
	if err := a.writeUDPReply(conn, reply); err != nil {
		return err
	}
	if !reply.Found {
		return nil
	}
	requesterData, err := a.store.Resolve(ctx, types.SUDPH, requester)
	if err != nil {
		return nil
	}
	if err := a.sendDialRequest(target, requester, requesterData); err != nil && !errors.Is(err, ErrNotConnected) {
		a.log.WithError(err).Debugf("Failed to ask %v to dial %v (UDP lookup)", target, requester)
	}
	return nil
}

func (a *API) writeUDPReply(conn net.Conn, reply addrresolver.UDPResolveReply) error {
	b, err := json.Marshal(reply)
	if err != nil {
		return err
	}
	_, err = conn.Write(b)
	return err
}
