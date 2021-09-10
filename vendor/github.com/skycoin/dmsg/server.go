package dmsg

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/disc"
	"github.com/skycoin/dmsg/netutil"
	"github.com/skycoin/dmsg/servermetrics"
)

// ServerConfig configues the Server
type ServerConfig struct {
	MaxSessions    int
	UpdateInterval time.Duration
}

// DefaultServerConfig returns the default server config.
func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		MaxSessions:    DefaultMaxSessions,
		UpdateInterval: DefaultUpdateInterval,
	}
}

// Server represents a dsmg server entity.
type Server struct {
	EntityCommon

	m servermetrics.Metrics

	ready     chan struct{} // Closed once dmsg.Server is serving.
	readyOnce sync.Once

	done chan struct{}
	once sync.Once
	wg   sync.WaitGroup

	// Public TCP address which the dmsg server advertises itself as.
	// This should only be set once. Once set, addrDone closes.
	addr     string
	addrDone chan struct{}

	maxSessions int
}

// NewServer creates a new dmsg server entity.
func NewServer(pk cipher.PubKey, sk cipher.SecKey, dc disc.APIClient, conf *ServerConfig, m servermetrics.Metrics) *Server {
	if conf == nil {
		conf = DefaultServerConfig()
	}
	if m == nil {
		m = servermetrics.NewEmpty()
	}
	log := logging.MustGetLogger("dmsg_server")

	s := new(Server)
	s.EntityCommon.init(pk, sk, dc, log, conf.UpdateInterval)
	s.m = m
	s.ready = make(chan struct{})
	s.done = make(chan struct{})
	s.addrDone = make(chan struct{})
	s.maxSessions = conf.MaxSessions
	s.setSessionCallback = func(ctx context.Context, sessionCount int) error {
		return s.updateServerEntry(ctx, s.AdvertisedAddr(), s.maxSessions)
	}
	s.delSessionCallback = func(ctx context.Context, sessionCount int) error {
		return s.updateServerEntry(ctx, s.AdvertisedAddr(), s.maxSessions)
	}
	return s
}

// GetSessions returns underlying sessions map.
func (s *Server) GetSessions() map[cipher.PubKey]*SessionCommon {
	s.sessionsMx.Lock()
	defer s.sessionsMx.Unlock()

	sessions := make(map[cipher.PubKey]*SessionCommon, len(s.sessions))
	for pk, session := range s.sessions {
		sessions[pk] = session
	}

	return sessions
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
	s.SetAdvertisedAddr(lis, &addr)

	log := s.log.
		WithField("advertised_addr", addr).
		WithField("local_pk", s.pk)

	log.Info("Serving server.")
	s.wg.Add(1)
	defer func() {
		log.Info("Stopped server.")
		s.wg.Done()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-s.done
		cancel()
		log.WithError(lis.Close()).Info("Stopping server...")
	}()

	if err := s.startUpdateEntryLoop(ctx); err != nil {
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

		// TODO(evanlinjin): Implement proper load-balancing.
		if s.SessionCount() >= s.maxSessions {
			s.log.
				WithField("max_sessions", s.maxSessions).
				WithField("remote_tcp", conn.RemoteAddr()).
				Debug("Max sessions is reached, but still accepting so clients who delegated us can still listen.")
		}

		s.wg.Add(1)
		go func(conn net.Conn) {
			defer func() {
				err := recover()
				if err != nil {
					log.Warnf("panic in handleSession: %+v", err)
				}
			}()
			s.handleSession(conn)
			s.wg.Done()
		}(conn)
	}
}

func (s *Server) startUpdateEntryLoop(ctx context.Context) error {
	err := netutil.NewDefaultRetrier(s.log).Do(ctx, func() error {
		return s.updateServerEntry(ctx, s.AdvertisedAddr(), s.maxSessions)
	})
	if err != nil {
		return err
	}

	go s.updateServerEntryLoop(ctx, s.AdvertisedAddr(), s.maxSessions)
	return nil
}

// AdvertisedAddr returns the TCP address in which the dmsg server is advertised by.
// This is the TCP address that should be contained within the dmsg discovery entry of this server.
func (s *Server) AdvertisedAddr() string {
	<-s.addrDone
	return s.addr
}

// SetAdvertisedAddr sets the advertised TCP address in which the dmsg server is advertised by.
// This should only be called once.
func (s *Server) SetAdvertisedAddr(lis net.Listener, addr *string) {
	if *addr == "" {
		s.log.Warn("We are using a local addr as the advertised addr. This should only be done in a local test env.")
		*addr = lis.Addr().String()
	}
	s.addr = *addr
	close(s.addrDone)
}

// Ready returns a chan which blocks until the server begins serving.
func (s *Server) Ready() <-chan struct{} {
	return s.ready
}

func (s *Server) handleSession(conn net.Conn) {
	log := logrus.FieldLogger(s.log.WithField("remote_tcp", conn.RemoteAddr()))

	dSes, err := makeServerSession(s.m, &s.EntityCommon, conn)
	if err != nil {
		if err := conn.Close(); err != nil {
			log.WithError(err).Debug("On handleSession() failure, close connection resulted in error.")
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
