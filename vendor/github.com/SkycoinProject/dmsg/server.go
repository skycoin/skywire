package dmsg

import (
	"context"
	"net"
	"sync"

	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/sirupsen/logrus"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/disc"
	"github.com/SkycoinProject/dmsg/netutil"
)

// Server represents a dsmg server entity.
type Server struct {
	EntityCommon

	ready     chan struct{} // Closed once dmsg.Server is serving.
	readyOnce sync.Once

	done chan struct{}
	once sync.Once
	wg   sync.WaitGroup
}

// NewServer creates a new dmsg server entity.
func NewServer(pk cipher.PubKey, sk cipher.SecKey, dc disc.APIClient) *Server {
	s := new(Server)
	s.EntityCommon.init(pk, sk, dc, logging.MustGetLogger("dmsg_server"))
	s.ready = make(chan struct{})
	s.done = make(chan struct{})
	return s
}

// Close implements io.Closer
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		close(s.done)
		s.wg.Wait()
	})
	return nil
}

// Serve serves the server.
func (s *Server) Serve(lis net.Listener, addr string) error {
	var log logrus.FieldLogger //nolint:gosimple
	log = s.log.WithField("local_addr", addr).WithField("local_pk", s.pk)

	log.Info("Serving server.")
	s.wg.Add(1)

	defer func() {
		log.Info("Stopped server.")
		s.wg.Done()
	}()

	go func() {
		<-s.done
		log.WithError(lis.Close()).
			Info("Stopping server, net.Listener closed.")
	}()

	log.Info("Updating discovery entry...")
	if addr == "" {
		addr = lis.Addr().String()
	}
	if err := s.updateEntryLoop(addr); err != nil {
		return err
	}

	log.Info("Accepting sessions...")
	s.readyOnce.Do(func() { close(s.ready) })
	for {
		conn, err := lis.Accept()
		if err != nil {
			// If server is closed, there is no error to report.
			if isClosed(s.done) {
				return nil
			}
			return err
		}

		s.wg.Add(1)
		go func(conn net.Conn) {
			s.handleSession(conn)
			s.wg.Done()
		}(conn)
	}
}

// Ready returns a chan which blocks until the server begins serving.
func (s *Server) Ready() <-chan struct{} {
	return s.ready
}

func (s *Server) updateEntryLoop(addr string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
		case <-s.done:
			cancel()
		}
	}()
	return netutil.NewDefaultRetrier(s.log).Do(ctx, func() error {
		return s.updateServerEntry(ctx, addr)
	})
}

func (s *Server) handleSession(conn net.Conn) {
	var log logrus.FieldLogger //nolint:gosimple
	log = s.log.WithField("remote_tcp", conn.RemoteAddr())

	dSes, err := makeServerSession(&s.EntityCommon, conn)
	if err != nil {
		log = log.WithError(err)
		if err := conn.Close(); err != nil {
			s.log.WithError(err).
				Debug("On handleSession() failure, close connection resulted in error.")
		}
		return
	}

	log = log.WithField("remote_pk", dSes.RemotePK())
	log.Info("Started session.")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		awaitDone(ctx, s.done)
		log.WithError(dSes.Close()).Info("Stopped session.")
	}()

	if s.setSession(ctx, dSes.SessionCommon) {
		dSes.Serve()
	}
	s.delSession(ctx, dSes.RemotePK())
	cancel()
}
