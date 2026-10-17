// Package visor pkg/visor/hosted_service.go
package visor

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// handOffHosted serves a skynet port request naming a service this visor runs
// under the service's own key. The stream goes to that service's listener on
// the port, so a caller with a transport to this visor needs no dmsg stream.
// It reports false when msg names this visor itself, which the caller serves.
func handOffHosted(log *logging.Logger, conn net.Conn, v *Visor, msg clientMsg) bool {
	var pk cipher.PubKey
	if err := pk.Set(msg.PK); err != nil {
		sendError(log, conn, fmt.Errorf("bad service key %q: %w", msg.PK, err))
		return true
	}
	if pk == v.conf.PK {
		return false
	}
	if _, ok := v.hostedKeys.Load(pk); !ok {
		sendError(log, conn, fmt.Errorf("service %s is not hosted here", pk))
		return true
	}
	if msg.Port <= 0 || msg.Port > 65535 {
		sendError(log, conn, fmt.Errorf("bad port %d", msg.Port))
		return true
	}
	ln, ok := dmsg.LocalListener(dmsg.Addr{PK: pk, Port: uint16(msg.Port)}) //nolint:gosec
	if !ok {
		sendError(log, conn, fmt.Errorf("service %s does not listen on port %d", pk, msg.Port))
		return true
	}
	origin, ok := remotePKFromForwardingConn(conn)
	if !ok {
		sendError(log, conn, fmt.Errorf("cannot identify the caller of service %s", pk))
		return true
	}
	reply, err := json.Marshal(serverReply{PK: msg.PK})
	if err != nil {
		closeConn(log, conn)
		return true
	}
	if _, err := conn.Write(reply); err != nil {
		closeConn(log, conn)
		return true
	}
	if err := ln.Hand(conn, dmsg.Addr{PK: origin}); err != nil {
		log.WithError(err).WithField("service", pk.String()).Warn("Hosted service did not take the stream")
		closeConn(log, conn)
		return true
	}
	log.WithField("service", pk.String()).WithField("port", msg.Port).WithField("caller", origin.String()).
		Debug("Handed a skynet stream to a hosted service")
	return true
}
