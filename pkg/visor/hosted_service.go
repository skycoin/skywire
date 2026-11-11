// Package visor pkg/visor/hosted_service.go
package visor

import (
	"encoding/json"
	"fmt"
	"net"
	"sync/atomic"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoaggregate"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
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
	attest, ok := v.hostedKeys.Load(pk)
	if !ok {
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
	reply, err := json.Marshal(serverReply{PK: msg.PK, Sig: attest.(cipher.Sig).Hex()})
	if err != nil {
		closeConn(log, conn)
		return true
	}
	if _, err := conn.Write(reply); err != nil {
		closeConn(log, conn)
		return true
	}
	if err := ln.Hand(conn, dmsg.Addr{PK: origin, Port: nextHandedPort()}); err != nil {
		log.WithError(err).WithField("service", pk.String()).Warn("Hosted service did not take the stream")
		closeConn(log, conn)
		return true
	}
	log.WithField("service", pk.String()).WithField("port", msg.Port).WithField("caller", origin.String()).
		Debug("Handed a skynet stream to a hosted service")
	return true
}

func (v *Visor) republishHosted() {
	if v.tpM == nil {
		return
	}
	v.tpM.SetHostedEntries(v.hostedEntries)
	v.tpM.RepublishTPDList()
}

// hostedEntries is the local link from this visor to each service it hosts.
func (v *Visor) hostedEntries() []*transport.Entry {
	var out []*transport.Entry
	v.hostedKeys.Range(func(k, _ any) bool {
		label := transport.LabelAutomatic
		if cxoaggregate.ConsumesSystemFeed(k.(cipher.PubKey)) {
			label = transport.LabelSysFeed
		}
		e := transport.MakeEntry(v.conf.PK, k.(cipher.PubKey), tptypes.LOCAL, label)
		out = append(out, &e)
		return true
	})
	return out
}

// handedPorts numbers handed-over streams the way a dmsg client numbers its
// ephemeral ports, so two streams from one caller keep distinct addresses.
var handedPorts atomic.Uint32

func nextHandedPort() uint16 {
	return uint16(49152 + handedPorts.Add(1)%16384) //nolint:gosec
}
