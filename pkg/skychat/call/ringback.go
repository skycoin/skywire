// Package call pkg/skychat/call/ringback.go c4-app-chat
package call

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// A ringback tone is what a caller hears while this endpoint rings — chosen by
// the person being called, the way a phone line can carry its own "caller
// tune" in place of the network's ring.
//
// It travels on a conn of its own. The callee names the tone in SigRinging (a
// hash, a type and a size — a few hundred bytes on the call's conn), and the
// caller, unless it already holds that tone for that peer, dials the callee's
// signaling port a second time and asks for it (SigTone). Sending it on the
// call's own conn was the obvious alternative and the wrong one: the answer is
// written to that conn too, and would have queued behind up to a megabyte of
// music — picking up would have taken as long as the tone took to arrive.
//
// Asking is only possible while the call rings, only for the call's own id and
// only by the call's own peer, so the tone is served to people who are, at
// that moment, calling — the same people who would hear it anyway — and to
// nobody who merely knows the key.

// MaxRingbackSize bounds a ringback tone: about a minute of 128 kbit/s audio,
// enough for any tone, and small enough to fetch within the first seconds of
// a ring.
const MaxRingbackSize = 1 << 20

// toneChunk is the tone bytes carried by one SigTone frame. Base64 in JSON
// inflates it by a third, which keeps a frame well inside sigMaxLen.
const toneChunk = 32 << 10

// toneFetchBudget bounds the caller's whole fetch, and toneServeBudget the
// callee's side of it. A fetch that takes longer than the ring is of no use to
// the call it was for, and a slow one still caches the tone for the next.
const (
	toneFetchBudget = 45 * time.Second
	toneServeBudget = 60 * time.Second
)

// toneFetchesPerCall caps how often one ringing call may ask for the tone: a
// retry after a dropped conn is fair, a loop is not.
const toneFetchesPerCall = 2

// toneCacheSize bounds the peer tones a caller keeps, least recently used out.
const toneCacheSize = 32

// ErrToneRefused is what a tone request answered with a refusal returns.
var ErrToneRefused = errors.New("voice: ringback tone refused")

// tone is one ringback tone.
type tone struct {
	data []byte
	mime string
	hash string
	used time.Time
}

func toneHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// toneMimes are the formats a ringback tone may be, each mapped to the type it
// is served as. A whitelist because the type came from another visor and ends
// up as a Content-Type on a page the caller's UI is served from: a peer that
// could pick "text/html" would pick it.
var toneMimes = map[string]string{
	"audio/mpeg":      "audio/mpeg",
	"audio/mp3":       "audio/mpeg",
	"audio/ogg":       "audio/ogg",
	"application/ogg": "audio/ogg",
	"audio/opus":      "audio/ogg",
	"audio/webm":      "audio/webm",
	"video/webm":      "audio/webm",
	"audio/wav":       "audio/wav",
	"audio/x-wav":     "audio/wav",
	"audio/wave":      "audio/wav",
	"audio/vnd.wave":  "audio/wav",
	"audio/mp4":       "audio/mp4",
	"audio/m4a":       "audio/mp4",
	"audio/x-m4a":     "audio/mp4",
	"audio/aac":       "audio/aac",
	"audio/flac":      "audio/flac",
	"audio/x-flac":    "audio/flac",
}

// NormalizeToneMime returns the type a ringback tone of the given type is
// stored and served as, or "" when it is not an audio format a tone may be.
func NormalizeToneMime(mime string) string {
	base := strings.ToLower(strings.TrimSpace(mime))
	if i := strings.IndexByte(base, ';'); i >= 0 {
		base = strings.TrimSpace(base[:i])
	}
	return toneMimes[base]
}

// SetRingback sets the tone callers hear while this endpoint rings; empty data
// clears it. Only calls that start ringing afterwards carry the change.
func (m *Manager) SetRingback(data []byte, mime string) error {
	if len(data) == 0 {
		m.mu.Lock()
		m.ownTone = nil
		m.mu.Unlock()
		return nil
	}
	if len(data) > MaxRingbackSize {
		return fmt.Errorf("voice: ringback tone is %d bytes, the limit is %d", len(data), MaxRingbackSize)
	}
	kind := NormalizeToneMime(mime)
	if kind == "" {
		return fmt.Errorf("voice: %q is not an audio format a ringback tone can be", mime)
	}
	t := &tone{data: append([]byte(nil), data...), mime: kind, hash: toneHash(data)}
	m.mu.Lock()
	m.ownTone = t
	m.mu.Unlock()
	return nil
}

// Ringback returns this endpoint's own ringback tone; nil data when none is
// set.
func (m *Manager) Ringback() (data []byte, mime string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ownTone == nil {
		return nil, ""
	}
	return m.ownTone.data, m.ownTone.mime
}

// DialRingback returns the ringback tone the peer of an outbound call plays,
// once its bytes are here (Dial.Ringback). ok is false until then, and for a
// peer that plays none — the caller then plays an ordinary ring of its own.
func (m *Manager) DialRingback(callID string) (data []byte, mime string, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.dialing[callID]
	if d == nil || !d.toneReady {
		return nil, "", false
	}
	t := m.tones[d.peer]
	if t == nil || t.hash != d.tone {
		return nil, "", false
	}
	return t.data, t.mime, true
}

// cacheToneLocked keeps a peer's tone, evicting the least recently used past
// toneCacheSize. m.mu must be held.
func (m *Manager) cacheToneLocked(peer cipher.PubKey, t *tone) {
	m.tones[peer] = t
	if len(m.tones) <= toneCacheSize {
		return
	}
	peers := make([]cipher.PubKey, 0, len(m.tones))
	for pk := range m.tones {
		peers = append(peers, pk)
	}
	sort.Slice(peers, func(i, j int) bool { return m.tones[peers[i]].used.Before(m.tones[peers[j]].used) })
	for _, pk := range peers[:len(peers)-toneCacheSize] {
		delete(m.tones, pk)
	}
}

// fetchTone fetches the ringback tone a ringing callee announced and, when it
// arrives intact, caches it and marks the call's tone ready. Runs to the end
// even if the call is answered first: the next call to this peer then has it.
func (m *Manager) fetchTone(callID string, peer cipher.PubKey, r Sig) {
	ctx, cancel := context.WithTimeout(context.Background(), toneFetchBudget)
	defer cancel()
	data, err := m.sig.FetchTone(ctx, peer, callID, r.Tone, r.ToneSize)
	if err != nil {
		m.log.WithError(err).WithField("call", callID).Debug("voice: ringback tone not fetched; playing the plain ring")
		return
	}
	t := &tone{data: data, mime: NormalizeToneMime(r.ToneMime), hash: r.Tone, used: time.Now()}
	if t.mime == "" {
		t.mime = "application/octet-stream"
	}
	m.mu.Lock()
	m.cacheToneLocked(peer, t)
	if d := m.dialing[callID]; d != nil && d.tone == r.Tone {
		d.toneReady = true
	}
	m.mu.Unlock()
}

// handleTone serves this endpoint's ringback tone to the caller of a call that
// is ringing here, in toneChunk pieces, then closes the conn.
func (m *Manager) handleTone(req Sig, conn net.Conn) {
	defer conn.Close() //nolint:errcheck
	// A backstop rather than a write deadline, which some carriers ignore.
	backstop := time.AfterFunc(toneServeBudget, func() { _ = conn.Close() }) //nolint:errcheck
	defer backstop.Stop()

	refuse := func(reason string) {
		_ = writeSig(conn, Sig{Type: SigDecline, CallID: req.CallID, FromPK: m.cfg.LocalPK, Reason: reason}) //nolint:errcheck
	}
	m.mu.Lock()
	rc := m.ringing[req.CallID]
	t := m.ownTone
	var caller cipher.PubKey
	if rc != nil {
		caller = rc.inv.FromPK
	}
	m.mu.Unlock()
	if rc == nil {
		refuse("no such call")
		return
	}
	// Checked before the request is counted, or anyone could spend the
	// caller's allowance for it.
	if !isFromPeer(caller, req, conn) {
		refuse("not the caller")
		m.log.WithField("call", req.CallID).Warn("voice: refused a ringback tone request that did not come from the caller")
		return
	}
	m.mu.Lock()
	allowed := rc.toneFetches < toneFetchesPerCall
	rc.toneFetches++
	m.mu.Unlock()
	switch {
	case !allowed:
		refuse("tone already sent")
		return
	case t == nil || t.hash != req.Tone:
		refuse("no such tone")
		return
	}
	for off := 0; off < len(t.data); off += toneChunk {
		end := min(off+toneChunk, len(t.data))
		frame := Sig{Type: SigTone, CallID: req.CallID, FromPK: m.cfg.LocalPK,
			Tone: t.hash, ToneMime: t.mime, ToneSize: len(t.data), Data: t.data[off:end]}
		if err := writeSig(conn, frame); err != nil {
			m.log.WithError(err).WithField("call", req.CallID).Debug("voice: ringback tone send cut short")
			return
		}
	}
}

// FetchTone asks the peer, on a conn of its own, for the ringback tone it
// announced for callID, and returns its bytes once all size of them have
// arrived and hash to what was announced.
func (s *Signaler) FetchTone(ctx context.Context, peer cipher.PubKey, callID, hash string, size int) ([]byte, error) {
	if size <= 0 || size > MaxRingbackSize {
		return nil, fmt.Errorf("voice: ringback tone of %d bytes is out of bounds", size)
	}
	conn, err := s.dial(ctx, peer, s.port)
	if err != nil {
		return nil, fmt.Errorf("voice: ringback tone dial: %w", err)
	}
	defer conn.Close() //nolint:errcheck
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(dl) //nolint:errcheck // not every conn honors one; the AfterFunc still does
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() }) //nolint:errcheck
	defer stop()

	if err := writeSig(conn, Sig{Type: SigTone, CallID: callID, FromPK: s.localPK, Tone: hash}); err != nil {
		return nil, fmt.Errorf("voice: ringback tone request: %w", err)
	}
	buf := make([]byte, 0, size)
	for len(buf) < size {
		f, err := readSig(conn)
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("voice: ringback tone: %w", ctx.Err())
			}
			return nil, fmt.Errorf("voice: ringback tone: %w", err)
		}
		if f.Type != SigTone {
			return nil, fmt.Errorf("%w: %s", ErrToneRefused, f.Reason)
		}
		if f.Tone != hash || f.ToneSize != size || len(f.Data) == 0 || len(buf)+len(f.Data) > size {
			return nil, errors.New("voice: ringback tone arrived malformed")
		}
		buf = append(buf, f.Data...)
	}
	if toneHash(buf) != hash {
		return nil, errors.New("voice: ringback tone does not match its hash")
	}
	return buf, nil
}
