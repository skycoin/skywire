package skymail

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skymailbridge"
)

// Port is SMTP's port, on dmsg and skynet alike.
const Port = 25

// Networks a Service listens on, in the order it opens them.
const (
	NetDmsg   = "dmsg"
	NetSkynet = "skynet"
)

// Host is what a running mailbox needs from the visor it serves.
type Host struct {
	PK cipher.PubKey
	// Listen opens Port on one of the Networks.
	Listen func(network string) (net.Listener, error)
	// Dialers reach a peer's Port, keyed by address suffix.
	Dialers map[string]skymailbridge.Dialer
	// PeerPK names the authenticated remote PK of an inbound connection.
	PeerPK func(net.Conn) (cipher.PubKey, bool)
	// Sealer seals and opens envelopes as PK. Nil turns sealed mail off.
	Sealer Sealer
	// Peers are the visors this one manages as a hypervisor and its own
	// hypervisors, the default helpers and relay-for lists.
	Peers     func() (managed, hypervisors []cipher.PubKey)
	OnDeliver func(Delivered)
	Log       logrus.FieldLogger
}

// ServiceConfig starts a Service.
type ServiceConfig struct {
	Host
	// Dir is the Maildir root, which also holds SettingsFile.
	Dir string
	// Base are the limits SettingsFile overrides.
	Base Limits
	// Outbox queues mail for a visor that cannot take it yet.
	Outbox bool
}

// Service is a mailbox that serves Port: the receiving and sending side of
// the skymail app, apart from how the app reaches its visor.
type Service struct {
	cfg    ServiceConfig
	mb     *Mailbox
	relay  atomic.Pointer[Relay]
	cancel context.CancelFunc

	lisMu sync.Mutex
	lis   []net.Listener
	wg    sync.WaitGroup
}

// StartService opens the mailbox and serves it on every network the host
// can listen on. dmsg is required. A visor without skynet still takes
// mail over dmsg, as a .skynet address cannot reach it anyway.
func StartService(ctx context.Context, cfg ServiceConfig) (*Service, error) {
	if cfg.Log == nil {
		cfg.Log = logrus.NewEntry(logrus.New())
	}
	saved, err := LoadSettings(cfg.Dir)
	if err != nil {
		cfg.Log.WithError(err).Warn("skymail: unreadable " + SettingsFile + "; using the baseline")
	}
	s := &Service{cfg: cfg}
	s.relay.Store(&saved.Relay)
	mbCfg := Config{
		Dir:       cfg.Dir,
		PK:        cfg.PK,
		PeerPK:    cfg.PeerPK,
		Dialers:   cfg.Dialers,
		Limits:    saved.Over(cfg.Base),
		Outbox:    cfg.Outbox,
		OnDeliver: cfg.OnDeliver,
		Log:       cfg.Log,
	}
	if cfg.Sealer != nil && cfg.Outbox {
		mbCfg.Sealer = cfg.Sealer
		mbCfg.Helpers = s.Helpers
		mbCfg.RelayFor = func(pk cipher.PubKey) bool {
			r, _ := s.RelayFor()
			return slices.Contains(r, pk)
		}
	}
	if s.mb, err = Open(mbCfg); err != nil {
		return nil, err
	}
	dmsgLis, err := cfg.Listen(NetDmsg)
	if err != nil {
		return nil, fmt.Errorf("dmsg port %d: %w", Port, err)
	}
	ctx, s.cancel = context.WithCancel(ctx)
	s.serve(ctx, NetDmsg, dmsgLis)
	if l, err := cfg.Listen(NetSkynet); err != nil {
		cfg.Log.WithError(err).Warn("skymail: no skynet listener; mail arrives over dmsg only")
	} else {
		s.serve(ctx, NetSkynet, l)
	}
	s.wg.Add(2)
	go func() { defer s.wg.Done(); s.mb.RunExpiry(ctx) }()
	go func() { defer s.wg.Done(); s.mb.RunOutbox(ctx) }()
	lim := s.mb.Limits()
	cfg.Log.WithField("address", s.mb.Address("", Suffixes[0])).WithField("dir", cfg.Dir).
		WithField("max_total_size", lim.MaxTotalSize).WithField("max_age", lim.MaxAge).
		Info("Mailbox running")
	return s, nil
}

func (s *Service) serve(ctx context.Context, network string, l net.Listener) {
	s.lisMu.Lock()
	s.lis = append(s.lis, l)
	s.lisMu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.mb.Serve(ctx, l); err != nil && !errors.Is(err, net.ErrClosed) {
			s.cfg.Log.WithError(err).WithField("network", network).Warn("skymail: listener stopped")
		}
	}()
}

// Close stops serving and waits for the mailbox's loops, keeping its mail.
// Port is free again when it returns.
func (s *Service) Close() {
	s.cancel()
	s.lisMu.Lock()
	for _, l := range s.lis {
		_ = l.Close() //nolint:errcheck
	}
	s.lis = nil
	s.lisMu.Unlock()
	s.wg.Wait()
}

// Mailbox is the mailbox the Service serves.
func (s *Service) Mailbox() *Mailbox { return s.mb }

// Reload applies SettingsFile as it now stands, then offers queued mail
// to a helper that may be new.
func (s *Service) Reload() error {
	saved, err := LoadSettings(s.cfg.Dir)
	if err != nil {
		return err
	}
	s.mb.SetLimits(saved.Over(s.cfg.Base))
	s.relay.Store(&saved.Relay)
	if s.mb.HasOutbox() {
		return s.mb.Retry("")
	}
	return nil
}

// Helpers is who gets this mailbox's queued mail, and whether that is the
// default: the visors it manages as a hypervisor, which trust it already.
func (s *Service) Helpers() []cipher.PubKey {
	h, _ := s.helpers()
	return h
}

func (s *Service) helpers() ([]cipher.PubKey, bool) {
	if r := s.relay.Load(); r != nil && r.Helpers != nil {
		return *r.Helpers, false
	}
	if s.cfg.Peers == nil {
		return nil, true
	}
	managed, _ := s.cfg.Peers()
	return managed, true
}

// RelayFor is whose sealed mail this mailbox carries, and whether that is
// the default: its hypervisors, which can already do anything on it.
func (s *Service) RelayFor() ([]cipher.PubKey, bool) {
	if r := s.relay.Load(); r != nil && r.RelayFor != nil {
		return *r.RelayFor, false
	}
	if s.cfg.Peers == nil {
		return nil, true
	}
	_, hvs := s.cfg.Peers()
	return hvs, true
}

// Status is the state of a running mailbox.
type Status struct {
	Address     string          `json:"address"`
	AddressDmsg string          `json:"address_dmsg"`
	Dir         string          `json:"dir"`
	Limits      Limits          `json:"limits"`
	Usage       int64           `json:"usage"`
	Whitelist   []cipher.PubKey `json:"whitelist"`
	Unread      int             `json:"unread"`
	Total       int             `json:"total"`
	HasOutbox   bool            `json:"has_outbox"`
	Outbox      int             `json:"outbox"`
	Sealing     bool            `json:"sealing"`

	Helpers         []cipher.PubKey `json:"helpers"`
	HelpersDefault  bool            `json:"helpers_default"`
	RelayFor        []cipher.PubKey `json:"relay_for"`
	RelayForDefault bool            `json:"relay_for_default"`
}

// Status reports the mailbox's state now.
func (s *Service) Status() Status {
	mb := s.mb
	st := Status{
		Address:     mb.Address("", Suffixes[0]),
		AddressDmsg: mb.Address("", Suffixes[1]),
		Dir:         s.cfg.Dir,
		Limits:      mb.Limits(),
		Whitelist:   mb.Whitelist(),
		HasOutbox:   mb.HasOutbox(),
		Outbox:      mb.Queued(),
		Sealing:     mb.sealing(),
	}
	st.Usage, _ = mb.Usage() //nolint:errcheck // a status line, not a decision
	if st.HasOutbox {
		st.Helpers, st.HelpersDefault = s.helpers()
		st.RelayFor, st.RelayForDefault = s.RelayFor()
	}
	if inbox, err := mb.List(FolderInbox); err == nil {
		st.Total = len(inbox)
		for _, m := range inbox {
			if !m.Seen {
				st.Unread++
			}
		}
	}
	return st
}
