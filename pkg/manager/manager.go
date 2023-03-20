// Package manager pkg/manager/manager.go
package manager

import (
	"context"
	"errors"
	"fmt"

	"github.com/skycoin/dmsg/pkg/dmsg"
	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/setup"
)

// Manager manages the authenticated RPC connections to the visor
type Manager struct {
	dmsgC     *dmsg.Client
	log       *logging.Logger
	tm        *transport.Manager
	router    router.Router
	authNodes []cipher.PubKey
	localSK   cipher.SecKey
}

// New makes a Manager from configuration
func New(ctx context.Context, pk cipher.PubKey, sk cipher.SecKey, authNodes []cipher.PubKey, dmsgC *dmsg.Client, tm *transport.Manager, router router.Router,
	masterLogger *logging.MasterLogger) (*Manager, error) {
	log := masterLogger.PackageLogger("manager")
	log.WithField("local_pk", pk).Debug("Connecting to the dmsg network.")

	select {
	case <-dmsgC.Ready():
		log.WithField("local_pk", pk).Debug("Connected!")
		tl := &Manager{
			dmsgC:     dmsgC,
			log:       log,
			tm:        tm,
			router:    router,
			authNodes: authNodes,
			localSK:   sk,
		}
		return tl, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("failed to connect to dmsg network")
	}
}

// ListenAndServe listens for a dmsg connection and serves Management API rpc to trusted nodes
func (m *Manager) ListenAndServe(ctx context.Context) {
	m.log.WithField("dmsg_port", skyenv.DmsgManagerRPCPort).Debug("starting listener")
	lis, err := m.dmsgC.Listen(skyenv.DmsgManagerRPCPort)
	if err != nil {
		m.log.WithError(err).Error("failed to listen")
	}
	go func() {
		<-ctx.Done()
		if err := lis.Close(); err != nil {
			m.log.WithError(err).Warn("Dmsg listener closed with non-nil error.")
		}
	}()

	m.log.Error("Listening")
	m.log.WithField("dmsg_port", skyenv.DmsgManagerRPCPort).Debug("Accepting dmsg streams.")
	for {
		conn, err := lis.AcceptStream()
		if err != nil {
			log := m.log.WithError(err)
			if errors.Is(err, dmsg.ErrEntityClosed) {
				log.Debug("Dmsg client stopped serving.")
				break
			}
			log.Error("Failed to accept")
			break
		}
		remotePK := conn.RawRemoteAddr().PK
		m.log.Errorf("got one %v", remotePK)
		found := false
		for _, trusted := range m.authNodes {
			if trusted == remotePK {
				found = true
				break
			}
		}

		if !found {
			m.log.WithField("remote_conn", remotePK).WithField("authorized_nodes", m.authNodes).Debug("Closing connection")
			if err := conn.Close(); err != nil {
				m.log.WithError(err).Error("Failed to close stream")
			}
		}

		tpAPI := setup.NewAPI(m.tm, m.log, m.router)

		sharedSec, err := skycipher.ECDH(skycipher.PubKey(remotePK), skycipher.SecKey(m.localSK))
		if err != nil {
			m.log.WithError(err).Error("Failed to generate shared secret")
		}

		mgmtAPI := NewManagementInterface(tpAPI, remotePK, m.localSK, sharedSec)

		rpcS, err := newRPCServer(mgmtAPI, m.log)
		if err != nil {
			m.log.WithError(err).Error("failed to register rpc")
		}

		m.log.WithField("remote_conn", remotePK).Debug("Serving rpc")
		go rpcS.ServeConn(conn)
	}
}
