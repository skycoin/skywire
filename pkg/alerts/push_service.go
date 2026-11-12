// Package alerts pkg/alerts/push_service.go c3-vis-core
package alerts

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// maxPushSubscriptions bounds the subscribed browsers.
const maxPushSubscriptions = 20

// vapidKey loads or makes the VAPID key, saved beside alerts.json. s.mu
// is held. It is nil only when it cannot be made.
func (s *Service) vapidKey() *ecdsa.PrivateKey {
	if s.vapid != nil {
		return s.vapid
	}
	path := filepath.Join(s.dir, vapidFile)
	if s.dir != "" {
		if b, err := os.ReadFile(path); err == nil { //nolint:gosec
			raw, err := b64(strings.TrimSpace(string(b)))
			if err == nil {
				if k, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), raw); err == nil {
					s.vapid = k
					return k
				}
			}
			if s.Logf != nil {
				s.Logf("ignoring unreadable %s, making a new push key", path)
			}
		}
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil
	}
	if s.dir != "" {
		raw, err := k.Bytes()
		if err != nil {
			return nil
		}
		if err := writeFile(path, []byte(base64.RawURLEncoding.EncodeToString(raw)+"\n")); err != nil {
			if s.Logf != nil {
				s.Logf("saving push key: %v", err)
			}
			return nil
		}
	}
	s.vapid = k
	return k
}

// sender is a copy of Sender with the VAPID key.
func (s *Service) sender() *Sender {
	s.mu.Lock()
	defer s.mu.Unlock()
	snd := *s.Sender
	if len(s.settings.Push) > 0 {
		snd.VAPID = s.vapidKey()
	}
	return &snd
}

// PushAdd adds or replaces the subscription of one browser and returns it
// with its id.
func (s *Service) PushAdd(sub PushSubscription) (PushSubscription, error) {
	sub.Label = strings.TrimSpace(sub.Label)
	if len(sub.Label) > 64 {
		sub.Label = sub.Label[:64]
	}
	if err := sub.Validate(); err != nil {
		return sub, err
	}
	sub.ID = PushID(sub.Endpoint)
	sub.Created = time.Now().UTC().Truncate(time.Second)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.vapidKey() == nil {
		return sub, errors.New("no push key")
	}
	next := s.settings
	next.Push = slices.DeleteFunc(slices.Clone(next.Push), func(p PushSubscription) bool { return p.ID == sub.ID })
	if len(next.Push) >= maxPushSubscriptions {
		return sub, fmt.Errorf("at most %d push devices", maxPushSubscriptions)
	}
	next.Push = append(next.Push, sub)
	return sub, s.saveLocked(next)
}

// PushRemove removes the subscription id, or every one for "all", and
// returns how many it removed.
func (s *Service) PushRemove(id string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.settings
	n := len(next.Push)
	next.Push = slices.DeleteFunc(slices.Clone(next.Push), func(p PushSubscription) bool { return id == "all" || p.ID == id })
	n -= len(next.Push)
	if n == 0 {
		return 0, fmt.Errorf("no push device %q", id)
	}
	return n, s.saveLocked(next)
}

// dropGone removes the subscriptions the push service said are gone.
func (s *Service) dropGone(res []Result) {
	for _, r := range res {
		if r.gone == "" {
			continue
		}
		if _, err := s.PushRemove(r.gone); err == nil && s.Logf != nil {
			s.Logf("push device %s is gone, removed it", r.Target)
		}
	}
}
