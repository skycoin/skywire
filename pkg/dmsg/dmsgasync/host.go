// Package dmsgasync pkg/dmsg/dmsgasync/host.go c1-net-dmsg
package dmsgasync

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// Store backends.
const (
	StoreDisk   = "disk"
	StoreMemory = "memory"
)

// HostConfig configures an inbox host.
type HostConfig struct {
	// PK is the host's own key, which a record must name.
	PK cipher.PubKey
	// Store is StoreDisk (the default) or StoreMemory.
	Store string
	// Dir holds the disk store and the registrations. Required for StoreDisk.
	Dir    string
	Limits Limits
	// Accepts reports whether this host agrees to hold for a key. It is
	// asked when the key registers, and a key it refuses cannot register.
	Accepts func(cipher.PubKey) bool
	Log     *logging.Logger
}

type registration struct {
	Record Record `json:"record"`
	Policy Policy `json:"policy"`
}

// Host serves the dmsg-async protocol for the keys registered with it.
type Host struct {
	cfg   HostConfig
	store *store
	log   *logging.Logger

	mu   sync.RWMutex
	regs map[cipher.PubKey]registration
}

// NewHost opens the store and loads the registrations a disk host kept.
func NewHost(cfg HostConfig) (*Host, error) {
	if cfg.PK.Null() {
		return nil, errors.New("dmsg-async: host key required")
	}
	if cfg.Accepts == nil {
		return nil, errors.New("dmsg-async: host needs an Accepts rule")
	}
	if cfg.Log == nil {
		cfg.Log = logging.MustGetLogger("dmsg-async")
	}
	dir := ""
	switch cfg.Store {
	case "", StoreDisk:
		if cfg.Dir == "" {
			return nil, errors.New("dmsg-async: the disk store needs a dir")
		}
		dir = filepath.Join(cfg.Dir, "messages")
	case StoreMemory:
	default:
		return nil, fmt.Errorf("dmsg-async: unknown store %q", cfg.Store)
	}
	st, err := newStore(cfg.Limits, dir, time.Now())
	if err != nil {
		return nil, err
	}
	h := &Host{cfg: cfg, store: st, log: cfg.Log, regs: map[cipher.PubKey]registration{}}
	if dir != "" {
		h.loadRegs()
	}
	return h, nil
}

// Limits returns the host's limits with defaults applied.
func (h *Host) Limits() Limits { return h.store.lim }

// Stats reports what the store holds.
func (h *Host) Stats() Stats { return h.store.stats() }

// Registered lists the keys registered with this host.
func (h *Host) Registered() []cipher.PubKey {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]cipher.PubKey, 0, len(h.regs))
	for pk := range h.regs {
		out = append(out, pk)
	}
	return out
}

// Serve answers streams from l until it closes, and sweeps expired messages.
func (h *Host) Serve(ctx context.Context, l net.Listener) error {
	go h.sweepLoop(ctx)
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		remote, ok := RemotePK(conn)
		if !ok {
			_ = conn.Close() //nolint:errcheck
			continue
		}
		go h.Handle(conn, remote)
	}
}

// RemotePK is the authenticated key at the other end of a dmsg stream.
func RemotePK(conn net.Conn) (cipher.PubKey, bool) {
	switch a := conn.RemoteAddr().(type) {
	case dmsg.Addr:
		return a.PK, !a.PK.Null()
	case *dmsg.Addr:
		return a.PK, a != nil && !a.PK.Null()
	}
	return cipher.PubKey{}, false
}

func (h *Host) sweepLoop(ctx context.Context) {
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			h.store.sweep(now)
			h.dropExpiredRegs(now)
		}
	}
}

// Handle runs one operation from remote on conn and closes it. remote MUST be
// the key the stream authenticated.
func (h *Host) Handle(conn net.Conn, remote cipher.PubKey) {
	defer conn.Close()                              //nolint:errcheck
	_ = conn.SetDeadline(time.Now().Add(opTimeout)) //nolint:errcheck
	var req request
	if err := readJSON(conn, &req); err != nil {
		_ = writeJSON(conn, errReply(ErrBadRequest)) //nolint:errcheck
		return
	}
	now := time.Now()
	var rep reply
	switch req.Op {
	case OpRegister:
		rep = h.register(remote, req, now)
	case OpUnregister:
		rep = h.unregister(remote)
	case OpRecord:
		rep = h.record(req.PK, now)
	case OpDeposit:
		rep = h.deposit(conn, remote, req, now)
	case OpFetch:
		h.fetch(conn, remote, req)
		return
	case OpAck:
		rep = reply{OK: true, Deleted: h.store.ack(remote, req.IDs)}
	default:
		rep = errReply(ErrBadRequest)
	}
	_ = writeJSON(conn, rep) //nolint:errcheck
}

func (h *Host) register(remote cipher.PubKey, req request, now time.Time) reply {
	if req.Record == nil {
		return errReply(ErrBadRecord)
	}
	rec := *req.Record
	if rec.PK != remote {
		return errReply(fmt.Errorf("%w: record is not for the asking key", ErrBadRecord))
	}
	if err := rec.Verify(now); err != nil {
		return errReply(err)
	}
	if !rec.Names(h.cfg.PK) {
		return errReply(fmt.Errorf("%w: record does not name this host", ErrBadRecord))
	}
	if !h.cfg.Accepts(remote) {
		return errReply(ErrNotHosted)
	}
	if rec.MaxMessage > h.store.lim.MaxMessage {
		return errReply(fmt.Errorf("%w: record asks for %d, host takes %d", ErrTooBig, rec.MaxMessage, h.store.lim.MaxMessage))
	}
	var pol Policy
	if req.Policy != nil {
		pol = *req.Policy
	}
	h.mu.Lock()
	if old, ok := h.regs[remote]; ok && old.Record.Seq > rec.Seq {
		h.mu.Unlock()
		return errReply(fmt.Errorf("%w: older than the registered record", ErrBadRecord))
	}
	h.regs[remote] = registration{Record: rec, Policy: pol}
	h.mu.Unlock()
	h.saveRegs()
	return reply{OK: true}
}

func (h *Host) unregister(remote cipher.PubKey) reply {
	h.mu.Lock()
	_, ok := h.regs[remote]
	delete(h.regs, remote)
	h.mu.Unlock()
	if !ok {
		return errReply(ErrNotHosted)
	}
	h.saveRegs()
	return reply{OK: true, Dropped: h.store.drop(remote)}
}

func (h *Host) record(pk cipher.PubKey, now time.Time) reply {
	reg, ok := h.active(pk, now)
	if !ok {
		return errReply(ErrNotHosted)
	}
	rec := reg.Record
	return reply{OK: true, Record: &rec}
}

// active returns pk's registration while it is unexpired. Accepts is checked
// at registration only: a managed visor that goes offline is exactly the
// key the host must keep holding for.
func (h *Host) active(pk cipher.PubKey, now time.Time) (registration, bool) {
	h.mu.RLock()
	reg, ok := h.regs[pk]
	h.mu.RUnlock()
	if !ok || !now.Before(reg.Record.Expires) {
		return registration{}, false
	}
	return reg, true
}

func (h *Host) deposit(conn net.Conn, from cipher.PubKey, req request, now time.Time) reply {
	reg, ok := h.active(req.To, now)
	if !ok {
		return errReply(ErrNotHosted)
	}
	if !reg.Policy.Allows(from) {
		return errReply(ErrNotAllowed)
	}
	limit := h.store.lim.MaxMessage
	if reg.Record.MaxMessage > 0 && reg.Record.MaxMessage < limit {
		limit = reg.Record.MaxMessage
	}
	if req.Size < 0 || req.Size > limit {
		return errReply(ErrTooBig)
	}
	if req.Ref != "" {
		if b, err := hex.DecodeString(req.Ref); err != nil || len(b) != refLen {
			return errReply(ErrBadRequest)
		}
	}
	// Tell the sender to go ahead before it spends the payload's bandwidth.
	if err := writeJSON(conn, reply{OK: true}); err != nil {
		return errReply(ErrInternal)
	}
	payload, err := readFrame(conn, req.Size)
	if err != nil || len(payload) != req.Size {
		return errReply(ErrBadRequest)
	}
	id, err := h.store.deposit(from, req.To, req.Port, req.Ref, payload, reg.Policy, now)
	if err != nil {
		return errReply(err)
	}
	return reply{OK: true, ID: id}
}

func (h *Host) fetch(conn net.Conn, remote cipher.PubKey, req request) {
	msgs, more, err := h.store.fetch(remote, req.Ports)
	if err != nil {
		_ = writeJSON(conn, errReply(err)) //nolint:errcheck
		return
	}
	heads := make([]Header, len(msgs))
	for i, m := range msgs {
		heads[i] = m.Header
	}
	if err := writeJSON(conn, reply{OK: true, Messages: heads, More: more}); err != nil {
		return
	}
	for _, m := range msgs {
		if err := writeFrame(conn, m.Payload); err != nil {
			return
		}
	}
}

func (h *Host) regsPath() string { return filepath.Join(h.cfg.Dir, "registrations.json") }

func (h *Host) saveRegs() {
	if h.store.dir == "" {
		return
	}
	h.mu.RLock()
	list := make([]registration, 0, len(h.regs))
	for _, r := range h.regs {
		list = append(list, r)
	}
	h.mu.RUnlock()
	b, err := json.Marshal(list)
	if err != nil {
		return
	}
	tmp := h.regsPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		h.log.WithError(err).Warn("dmsg-async: save registrations")
		return
	}
	if err := os.Rename(tmp, h.regsPath()); err != nil {
		h.log.WithError(err).Warn("dmsg-async: save registrations")
	}
}

func (h *Host) loadRegs() {
	b, err := os.ReadFile(h.regsPath())
	if err != nil {
		return
	}
	var list []registration
	if err := json.Unmarshal(b, &list); err != nil {
		h.log.WithError(err).Warn("dmsg-async: unreadable registrations, starting empty")
		return
	}
	now := time.Now()
	for _, r := range list {
		if r.Record.Verify(now) == nil && r.Record.Names(h.cfg.PK) {
			h.regs[r.Record.PK] = r
		}
	}
}

func (h *Host) dropExpiredRegs(now time.Time) {
	h.mu.Lock()
	n := 0
	for pk, r := range h.regs {
		if !now.Before(r.Record.Expires) {
			delete(h.regs, pk)
			n++
		}
	}
	h.mu.Unlock()
	if n > 0 {
		h.saveRegs()
	}
}

// Record returns pk's registered record while it is unexpired.
func (h *Host) Record(pk cipher.PubKey) (Record, bool) {
	reg, ok := h.active(pk, time.Now())
	return reg.Record, ok
}
