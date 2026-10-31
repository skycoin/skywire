// Package dmsg pkg/dmsg/dmsg/async_server.go c1-net-dmsg
package dmsg

import (
	"errors"
	"io"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// asyncSweepInterval is how often a server drops expired messages.
const asyncSweepInterval = 10 * time.Minute

// asyncAckTimeout bounds how long a server waits for a fetching client to
// ack, which covers the client handing each message to its handler.
const asyncAckTimeout = 2 * time.Minute

// startAsync opens the store and drops expired messages until the server
// closes. A store that cannot open leaves dmsg-async off; the server
// itself still serves.
func (s *Server) startAsync(cfg AsyncStoreConfig) {
	st, err := newAsyncStore(cfg, s.log)
	if err != nil {
		s.log.WithError(err).Error("dmsg-async: store did not open, leaving dmsg-async off")
		return
	}
	s.asyncStore, s.asyncInfo = st, st.cfg.info()
	n, b, r := st.stats()
	s.log.WithField("max_message", st.cfg.MaxMessage).WithField("ttl", st.cfg.TTL).
		WithField("dir", st.cfg.Dir).WithField("messages", n).WithField("bytes", b).WithField("recipients", r).
		Info("dmsg-async: holding messages for offline clients")
	go func() {
		t := time.NewTicker(asyncSweepInterval)
		defer t.Stop()
		for {
			select {
			case <-s.done:
				return
			case now := <-t.C:
				if n := st.sweep(now); n > 0 {
					s.log.WithField("expired", n).Debug("dmsg-async: dropped expired messages")
				}
			}
		}
	}()
}

// AsyncStats reports what a dmsg-async server holds; ok is false when it
// does not hold messages.
func (s *Server) AsyncStats() (messages int, bytes int64, recipients int, ok bool) {
	if s.asyncStore == nil {
		return 0, 0, 0, false
	}
	n, b, r := s.asyncStore.stats()
	return n, b, r, true
}

func asyncInfoEqual(a, b *disc.AsyncInfo) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

type deadliner interface {
	SetDeadline(time.Time) error
}

func setAsyncDeadline(str io.ReadWriteCloser, d time.Duration) {
	if dl, ok := str.(deadliner); ok {
		_ = dl.SetDeadline(time.Now().Add(d)) //nolint:errcheck // a stream without deadlines still works
	}
}

// serveAsync runs one dmsg-async operation on str for the client that
// signed req. The session authenticated that client, so the PK a deposit
// is from and the PK a fetch is for are the session's own.
func (ss *ServerSession) serveAsync(log logrus.FieldLogger, str io.ReadWriteCloser, req StreamRequest, relayed bool) error {
	defer str.Close() //nolint:errcheck
	reply := func(r asyncReply) error {
		setAsyncDeadline(str, asyncFrameTimeout)
		return writeAsyncJSON(str, r)
	}
	fail := func(err error) error {
		if werr := reply(asyncReply{Code: asyncErrCode(err), Error: err.Error()}); werr != nil {
			return werr
		}
		return nil
	}
	st := ss.entity.asyncStore
	if st == nil {
		return fail(ErrAsyncDisabled)
	}
	if relayed {
		// A relay would fetch, or spend the sender quota of, a key that is
		// not its own session's.
		return fail(ErrAsyncRelayed)
	}
	me := req.SrcAddr.PK
	log = log.WithField("client_pk", me)

	var op asyncOp
	setAsyncDeadline(str, asyncFrameTimeout)
	if err := readAsyncJSON(str, &op); err != nil {
		if errors.Is(err, ErrAsyncBadRequest) {
			return fail(err)
		}
		return err
	}
	switch op.Op {
	case asyncOpDeposit:
		if op.Size < 0 || op.Size > st.cfg.MaxMessage {
			return fail(ErrAsyncTooBig)
		}
		setAsyncDeadline(str, asyncFrameTimeout*asyncPayloadReadTimeoutFactor)
		payload, err := readAsyncFrame(str, st.cfg.MaxMessage)
		if err != nil {
			return err
		}
		if len(payload) != op.Size {
			return fail(ErrAsyncBadRequest)
		}
		id, err := st.deposit(me, op.To, op.Port, payload, time.Now())
		if err != nil {
			log.WithError(err).WithField("to", op.To).Debug("dmsg-async: deposit refused")
			return fail(err)
		}
		log.WithField("to", op.To).WithField("port", op.Port).WithField("bytes", len(payload)).WithField("id", id).
			Debug("dmsg-async: stored")
		return reply(asyncReply{OK: true, ID: id})

	case asyncOpFetch:
		msgs, more := st.fetch(me, op.Ports)
		heads := make([]AsyncHeader, len(msgs))
		for i, m := range msgs {
			heads[i] = m.AsyncHeader
		}
		if err := reply(asyncReply{OK: true, Messages: heads, More: more}); err != nil {
			return err
		}
		for _, m := range msgs {
			setAsyncDeadline(str, asyncFrameTimeout*asyncPayloadReadTimeoutFactor)
			if err := writeAsyncFrame(str, m.Payload); err != nil {
				return err
			}
		}
		if len(msgs) == 0 {
			return nil
		}
		var ack asyncOp
		setAsyncDeadline(str, asyncAckTimeout)
		if err := readAsyncJSON(str, &ack); err != nil {
			// Nothing is deleted without an ack; the messages wait for the
			// next fetch.
			return err
		}
		if ack.Op != asyncOpAck {
			return fail(ErrAsyncBadRequest)
		}
		n := st.ack(me, ack.IDs)
		log.WithField("fetched", len(msgs)).WithField("deleted", n).Debug("dmsg-async: fetched")
		return reply(asyncReply{OK: true, Deleted: n})
	}
	return fail(ErrAsyncBadRequest)
}
