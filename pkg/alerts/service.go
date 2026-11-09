package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// CheckInterval is how often the watched set is checked.
const CheckInterval = 30 * time.Second

// maxAttempts bounds retries of an event no channel accepted.
const maxAttempts = 5

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
}

type pending struct {
	ev       Event
	attempts int
}

// Service checks the watched visors and delivers alerts.
type Service struct {
	Sender *Sender
	// Targets returns the current liveness of every watched visor.
	Targets func(ctx context.Context, s Settings) []Target
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
}

const (
	settingsFile = "alerts.json"
	stateFile    = "alerts-state.json"
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
	alerted := map[cipher.PubKey]time.Time{}
	if dir != "" {
		if b, err := os.ReadFile(filepath.Join(dir, stateFile)); err == nil { //nolint:gosec
			if err := json.Unmarshal(b, &alerted); err != nil {
				errs = append(errs, err)
			}
		}
	}
	s.tracker = NewTracker(alerted)
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
	if s.dir != "" {
		b, err := json.MarshalIndent(next, "", "  ")
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(s.dir, settingsFile), b); err != nil {
			return err
		}
	}
	s.settings = next
	return nil
}

// Status returns the settings without the password and the watch state.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{
		Settings:     s.settings.Redacted(),
		Enabled:      s.settings.Enabled(),
		OfflineAfter: s.settings.Delay().String(),
		Watched:      s.tracker.States(),
		LastCheck:    s.lastCheck,
		LastSent:     s.lastSent,
		LastResults:  s.lastResults,
		Pending:      len(s.pending),
	}
}

// Test sends a test alert to every configured channel now.
func (s *Service) Test(ctx context.Context) ([]Result, error) {
	set := s.Settings()
	if !set.Enabled() {
		return nil, errors.New("no email address or webhook is set")
	}
	return s.Sender.Send(ctx, set, Event{Kind: KindTest, At: time.Now()}), nil
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
	events := s.tracker.Observe(now, set.Delay(), targets)
	queue := append(s.pending, make([]pending, 0, len(events))...)
	for _, ev := range events {
		queue = append(queue, pending{ev: ev})
	}
	s.pending = nil
	s.lastCheck = now
	alerted := s.tracker.Alerted()
	s.mu.Unlock()

	if len(events) > 0 {
		s.saveState(alerted)
	}
	var retry []pending
	for _, p := range queue {
		res := s.Sender.Send(ctx, set, p.ev)
		ok := false
		for _, r := range res {
			if r.Err == "" {
				ok = true
			} else if s.Logf != nil {
				s.Logf("alert %s for %s via %s %s failed: %s", p.ev.Kind, p.ev.Target.PK, r.Channel, r.Target, r.Err)
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

func (s *Service) saveState(alerted map[cipher.PubKey]time.Time) {
	if s.dir == "" {
		return
	}
	b, err := json.Marshal(alerted)
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
