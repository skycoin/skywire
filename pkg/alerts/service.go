// Package alerts pkg/alerts/service.go c3-vis-core
package alerts

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// CheckInterval is how often the watched set is checked.
const CheckInterval = 30 * time.Second

// maxAttempts bounds retries of an event no channel accepted.
const maxAttempts = 5

// LAN check results for a board alert.
const (
	DiagLANUp      = "board answers on the LAN, skywire is down"
	DiagLANDown    = "board does not answer on the LAN: powered off, hung or unplugged"
	DiagNoNeighbor = "no neighbor to check from"
	DiagNoLocalIP  = "board's LAN address is unknown, no LAN check"
)

// Status is what a read of the alerts returns. It never carries the relay
// password.
type Status struct {
	Settings     Settings      `json:"settings"`
	Enabled      bool          `json:"enabled"`
	OfflineAfter string        `json:"offline_after"`
	Watched      []TargetState `json:"watched"`
	LastCheck    time.Time     `json:"last_check,omitzero"`
	LastSent     time.Time     `json:"last_sent,omitzero"`
	LastResults  []Result      `json:"last_results,omitempty"`
	Pending      int           `json:"pending,omitempty"`
	// LastEvents are the most recent alerts, newest last.
	LastEvents []Event `json:"last_events,omitempty"`
	// PushPublicKey is the VAPID public key a browser subscribes with.
	PushPublicKey string `json:"push_public_key,omitempty"`
}

// maxLastEvents bounds Status.LastEvents.
const maxLastEvents = 10

type pending struct {
	ev       Event
	attempts int
}

// Service checks the watched visors and delivers alerts.
type Service struct {
	Sender *Sender
	// Targets returns the current liveness of every watched visor.
	Targets func(ctx context.Context, s Settings) []Target
	// Probe asks the visor via to check whether ip answers on its LAN.
	// Nil skips the LAN check.
	Probe func(ctx context.Context, via cipher.PubKey, ip string) (bool, error)
	// Self is the watching visor, a LAN neighbor when it shares a site.
	// Nil means it is never one.
	Self func() Target
	// Logf logs delivery failures. Nil discards.
	Logf func(format string, args ...any)

	dir string

	mu          sync.Mutex
	settings    Settings
	tracker     *Tracker
	pending     []pending
	lastCheck   time.Time
	lastSent    time.Time
	lastResults []Result
	lastEvents  []Event
	vapid       *ecdsa.PrivateKey
}

const (
	settingsFile = "alerts.json"
	stateFile    = "alerts-state.json"
	vapidFile    = "alerts-vapid.key"
)

// NewService returns a Service whose settings and alert state live in
// dir. base is the config's alerts block, used until settings are saved
// at runtime.
func NewService(dir string, base *Settings, sender *Sender) (*Service, error) {
	s := &Service{Sender: sender, dir: dir}
	if base != nil {
		s.settings = *base
	}
	var errs []error
	if dir != "" {
		if b, err := os.ReadFile(filepath.Join(dir, settingsFile)); err == nil { //nolint:gosec
			var set Settings
			if err := json.Unmarshal(b, &set); err != nil {
				errs = append(errs, err)
			} else {
				s.settings = set
			}
		}
	}
	saved := map[cipher.PubKey]Saved{}
	if dir != "" {
		if b, err := os.ReadFile(filepath.Join(dir, stateFile)); err == nil { //nolint:gosec
			if err := json.Unmarshal(b, &saved); err != nil {
				errs = append(errs, err)
				saved = map[cipher.PubKey]Saved{}
			}
		}
	}
	s.tracker = NewTracker(saved)
	return s, errors.Join(errs...)
}

// Settings returns the current settings, password included.
func (s *Service) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// SetSettings validates, applies and saves new settings. An empty relay
// password keeps the current one.
func (s *Service) SetSettings(next Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next = Merge(&s.settings, next)
	if err := next.Validate(); err != nil {
		return err
	}
	return s.saveLocked(next)
}

// saveLocked saves and applies set. s.mu is held.
func (s *Service) saveLocked(set Settings) error {
	if s.dir != "" {
		b, err := json.MarshalIndent(set, "", "  ")
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(s.dir, settingsFile), b); err != nil {
			return err
		}
	}
	s.settings = set
	return nil
}

// Status returns the settings without the password and the watch state.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{
		Settings:      s.settings.Redacted(),
		Enabled:       s.settings.Enabled(),
		OfflineAfter:  s.settings.Delay().String(),
		Watched:       s.tracker.States(),
		LastCheck:     s.lastCheck,
		LastSent:      s.lastSent,
		LastResults:   s.lastResults,
		Pending:       len(s.pending),
		LastEvents:    append([]Event(nil), s.lastEvents...),
		PushPublicKey: VAPIDPublicKey(s.vapidKey()),
	}
}

// Test sends a test alert to every configured channel now.
func (s *Service) Test(ctx context.Context) ([]Result, error) {
	set := s.Settings()
	if !set.Enabled() {
		return nil, errors.New("no email address, webhook or push device is set")
	}
	res := s.sender().Send(ctx, set, Event{Kind: KindTest, At: time.Now()})
	s.dropGone(res)
	return res, nil
}

// Run checks every interval until ctx is done. Visors that went down while
// nothing watched get floor-grace plus the delay before an alert.
func (s *Service) Run(ctx context.Context, interval, grace time.Duration) {
	s.mu.Lock()
	s.tracker.Floor = time.Now().Add(grace)
	s.mu.Unlock()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Tick(ctx, time.Now())
		}
	}
}

// Tick runs one check at now and delivers what it causes.
func (s *Service) Tick(ctx context.Context, now time.Time) {
	set := s.Settings()
	if !set.Enabled() {
		return
	}
	var targets []Target
	if s.Targets != nil {
		targets = s.Targets(ctx, set)
	}
	s.mu.Lock()
	s.tracker.DiskWarn = set.DiskWarn()
	events := s.tracker.Observe(now, set.Delay(), targets)
	s.lastCheck = now
	saved := s.tracker.Saved()
	s.mu.Unlock()

	for i, ev := range events {
		if ev.Kind == KindOffline && ev.Scope == ScopeBoard {
			events[i].Diagnosis = joinNotes(s.lanCheck(ctx, ev.Target, targets), ev.Diagnosis)
		}
	}

	s.mu.Lock()
	queue := append(s.pending, make([]pending, 0, len(events))...)
	for _, ev := range events {
		queue = append(queue, pending{ev: ev})
	}
	s.pending = nil
	s.lastEvents = append(s.lastEvents, events...)
	if n := len(s.lastEvents) - maxLastEvents; n > 0 {
		s.lastEvents = append([]Event(nil), s.lastEvents[n:]...)
	}
	s.mu.Unlock()

	if len(events) > 0 {
		s.saveState(saved)
	}
	var retry []pending
	for _, p := range queue {
		res := s.sender().Send(ctx, set, p.ev)
		s.dropGone(res)
		ok := false
		for _, r := range res {
			if r.Err == "" {
				ok = true
			} else if s.Logf != nil {
				s.Logf("alert %s for %s via %s %s failed: %s", p.ev.Kind, displayName(p.ev.Target), r.Channel, r.Target, r.Err)
			}
		}
		s.mu.Lock()
		s.lastResults = res
		if ok {
			s.lastSent = time.Now()
		}
		s.mu.Unlock()
		if p.attempts++; !ok && p.attempts < maxAttempts {
			retry = append(retry, p)
		}
	}
	if len(retry) > 0 {
		s.mu.Lock()
		s.pending = append(retry, s.pending...)
		s.mu.Unlock()
	}
}

// lanCheck asks an online visor on the same LAN as the offline board
// whether the board still answers there.
func (s *Service) lanCheck(ctx context.Context, board Target, targets []Target) string {
	if s.Probe == nil {
		return ""
	}
	if board.LocalIP == "" {
		return DiagNoLocalIP
	}
	cands := targets
	if s.Self != nil {
		me := s.Self()
		me.Online = true
		cands = append([]Target{me}, targets...)
	}
	cands = Neighbors(board, cands)
	if len(cands) == 0 {
		return DiagNoNeighbor
	}
	var errs []string
	for _, n := range cands {
		ok, err := s.Probe(ctx, n.PK, board.LocalIP)
		if err != nil {
			errs = append(errs, displayName(n)+": "+err.Error())
			continue
		}
		res := DiagLANDown
		if ok {
			res = DiagLANUp
		}
		return res + " (checked from " + displayName(n) + ")"
	}
	return "LAN check failed: " + strings.Join(errs, "; ")
}

// Neighbors lists the online visors in targets at board's site: the same
// public IP and the same LAN subnet, a /24 for IPv4 and a /64 for IPv6.
func Neighbors(board Target, targets []Target) []Target {
	var out []Target
	for _, t := range targets {
		if t.Online && t.PK != board.PK && board.PublicIP != "" && t.PublicIP == board.PublicIP && sameLAN(t.LocalIP, board.LocalIP) {
			out = append(out, t)
		}
	}
	return out
}

func sameLAN(a, b string) bool {
	ia, ib := net.ParseIP(a), net.ParseIP(b)
	if ia == nil || ib == nil {
		return false
	}
	if a4, b4 := ia.To4(), ib.To4(); a4 != nil || b4 != nil {
		return a4 != nil && b4 != nil && a4.Mask(net.CIDRMask(24, 32)).Equal(b4.Mask(net.CIDRMask(24, 32)))
	}
	m := net.CIDRMask(64, 128)
	return ia.Mask(m).Equal(ib.Mask(m))
}

func joinNotes(notes ...string) string {
	var out []string
	for _, n := range notes {
		if n != "" {
			out = append(out, n)
		}
	}
	return strings.Join(out, "; ")
}

func (s *Service) saveState(saved map[cipher.PubKey]Saved) {
	if s.dir == "" {
		return
	}
	b, err := json.Marshal(saved)
	if err != nil {
		return
	}
	if err := writeFile(filepath.Join(s.dir, stateFile), b); err != nil && s.Logf != nil {
		s.Logf("saving alert state: %v", err)
	}
}

func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
