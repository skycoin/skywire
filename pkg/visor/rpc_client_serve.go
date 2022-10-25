// Package visor pkg/visor/rpc_client_serve.go
package visor

import (
	"context"
	"net"
	"net/rpc"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
)

func isDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// ServeRPCClient repetitively dials to a remote dmsg address and serves a RPC server to that address.
func ServeRPCClient(ctx context.Context, log logrus.FieldLogger, autoPeerIP string, dmsgC *dmsg.Client, rpcS *rpc.Server, rAddr dmsg.Addr, errCh chan<- error) {
	const maxBackoff = time.Second * 5
	retry := netutil.NewRetrier(log, netutil.DefaultInitBackoff, maxBackoff, netutil.DefaultTries, netutil.DefaultFactor)
	pubkey := cipher.PubKey{}
	for {
		var conn net.Conn
		err := retry.Do(ctx, func() (rErr error) {
			log.Info("Dialing...")
			addr := dmsg.Addr{PK: rAddr.PK, Port: rAddr.Port}
			if autoPeerIP != "" {
				hvkey, err := FetchHvPk(autoPeerIP)
				if err != nil {
					log.Error("error autopeering")
				} else {
					hvkey = strings.TrimSuffix(hvkey, "\n")
					hypervisorPKsSlice := strings.Split(hvkey, ",")
					for _, pubkeyString := range hypervisorPKsSlice {
						//use pubkey.Set as validation or to convert the string to a pubkey
						if err := pubkey.Set(pubkeyString); err != nil {
							log.Warnf("Cannot add %s PK as remote hypervisor PK due to: %s", pubkeyString, err)
							continue
						}
						addr.PK = pubkey
					}
				}
			}
			conn, rErr = dmsgC.Dial(ctx, addr)
			return rErr
		})
		if err != nil {
			if errCh != nil {
				log.WithError(err).Info("Pushed error into 'errCh'.")
				errCh <- err
			}
			log.WithError(err).Info("Stopped Serving.")
			return
		}
		if conn == nil {
			log.WithField("conn == nil", conn == nil).Warn("An unexpected occurrence happened.")
			continue
		}

		log.Info("Serving RPC client...")
		connCtx, cancel := context.WithCancel(ctx)
		go func() {
			rpcS.ServeConn(conn)
			cancel()
		}()
		<-connCtx.Done()

		log.WithError(conn.Close()).
			WithField("context_done", isDone(ctx)).
			Debug("Conn closed. Redialing...")
	}
}
