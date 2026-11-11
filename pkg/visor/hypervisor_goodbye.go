// Package visor pkg/visor/hypervisor_goodbye.go c3-vis-core
package visor

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/alerts"
	"github.com/skycoin/skywire/pkg/cipher"
)

// A visor tells a hypervisor why it is leaving before it disconnects. The
// hypervisor long polls HypervisorNotice over the RPC conn the visor serves
// it, so the goodbye needs no new channel. Older peers simply never ask.

// hvNoticeWait is how long one HypervisorNotice call waits for a goodbye,
// inside the hypervisor's 20s RPC call timeout.
const hvNoticeWait = 15 * time.Second

// hvByeWait bounds how long a goodbye waits for the hypervisor to take it
// and close the link.
const hvByeWait = 2 * time.Second

var errNotHypervisorLink = errors.New("HypervisorNotice is only served to a hypervisor")

// hvLink is the visor's side of its link to one hypervisor.
type hvLink struct {
	once   sync.Once
	reason string
	bye    chan struct{}
	// asked is set once the hypervisor has polled for a goodbye. A link
	// to an older hypervisor never sets it, so a goodbye does not wait.
	asked  atomic.Bool
	mu     sync.Mutex
	served <-chan struct{}
}

func newHVLink() *hvLink { return &hvLink{bye: make(chan struct{})} }

// notice waits up to wait for a goodbye and returns its reason, or "".
func (l *hvLink) notice(wait time.Duration) string {
	l.asked.Store(true)
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-l.bye:
		return l.reason
	case <-t.C:
		return ""
	}
}

// said reports whether a goodbye was given.
func (l *hvLink) said() bool {
	select {
	case <-l.bye:
		return true
	default:
		return false
	}
}

// serving records the conn being served now; done closes when it ends.
func (l *hvLink) serving(done <-chan struct{}) {
	l.mu.Lock()
	l.served = done
	l.mu.Unlock()
}

// goodbye hands the hypervisor reason and waits until it closes the link
// or hvByeWait passes. Only the first reason is sent.
func (l *hvLink) goodbye(reason string) {
	l.once.Do(func() {
		l.reason = reason
		close(l.bye)
	})
	if !l.asked.Load() {
		return
	}
	l.mu.Lock()
	served := l.served
	l.mu.Unlock()
	if served == nil {
		return
	}
	t := time.NewTimer(hvByeWait)
	defer t.Stop()
	select {
	case <-served:
	case <-t.C:
	}
}

func (v *Visor) setHVLink(pk cipher.PubKey, l *hvLink) {
	v.hvLinksMu.Lock()
	defer v.hvLinksMu.Unlock()
	if v.hvLinks == nil {
		v.hvLinks = map[cipher.PubKey]*hvLink{}
	}
	v.hvLinks[pk] = l
}

// sayGoodbye tells the given hypervisors, or all of them when pks is
// nil, why this visor is leaving. The goodbyes run in parallel.
func (v *Visor) sayGoodbye(reason string, pks ...cipher.PubKey) {
	v.hvLinksMu.Lock()
	var links []*hvLink
	for pk, l := range v.hvLinks {
		if pks == nil || slices.Contains(pks, pk) {
			links = append(links, l)
		}
	}
	v.hvLinksMu.Unlock()
	var wg sync.WaitGroup
	for _, l := range links {
		wg.Add(1)
		go func(l *hvLink) {
			defer wg.Done()
			l.goodbye(reason)
		}(l)
	}
	wg.Wait()
}

// HypervisorNotice implements visorapi.API. It is answered only on the RPC
// server a visor runs for one of its hypervisors, see RPC.HypervisorNotice.
func (v *Visor) HypervisorNotice() (string, error) {
	return "", errNotHypervisorLink
}

// HypervisorNotice waits for the goodbye this visor owes the calling
// hypervisor: "" for none yet, alerts.StateDetached or
// alerts.StateRestarting. Not logged, the hypervisor calls it in a loop.
func (r *RPC) HypervisorNotice(_ *struct{}, out *string) error {
	if r.link == nil {
		return errNotHypervisorLink
	}
	*out = r.link.notice(hvNoticeWait)
	return nil
}

// Goodbye reasons.
const (
	byeDetached   = alerts.StateDetached
	byeRestarting = alerts.StateRestarting
)
