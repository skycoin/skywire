// Package group pkg/skychat/group/catalog.go c4-app-chat
// the discovery catalog: "what does this visor host that it wants found?"
//
// # Why this is separate from the probe
//
// probe.go answers a question about a group ID you already hold. This
// answers a question about a PUBLIC KEY, and that is a categorically
// different disclosure: a probe confirms one 122-bit identifier somebody
// already had, a catalog turns one key into a list. So it is not the same
// request with an empty field — it is its own frame, gated on its own
// per-group opt-in (Record.Listed), and it will never mention a group
// whose host did not tick the box.
//
// Both live on the same well-known port because both are "ask a visor
// about its groups" and neither mutates anything; one listener is simpler
// than two.
//
// # What a catalog entry is for
//
// Channels. A broadcast channel is the one shape here that is useless
// without discovery: a group gets its members from people who already know
// each other, but a channel wants an audience it has not met, and until now
// the only way to reach one was for someone to hand you a link. Groups may
// be listed too — the mechanism does not care — but the default is off and
// the reason it exists is channels.
//
// # What it deliberately is not
//
// Not a network-wide index. There is no aggregator, no crawl, no
// registration: a catalog is served by the host itself and answers only
// for the host's own groups, so discovering anything still starts from a
// public key somebody gave you. That keeps the trust model identical to
// the rest of skychat — you learn about a visor from a human — while
// removing the part that was genuinely painful, which was needing a fresh
// invite link per person for something meant to be public.
package group

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skychat/message"
)

// Catalog frame kinds. Distinct from the join frames so the probe
// listener can dispatch on the discriminator alone.
const (
	frameKindCatalogRequest  = "catalog_request"
	frameKindCatalogResponse = "catalog_response"
)

// maxCatalogEntries caps one answer.
//
// A bound rather than paging: a visor hosting more than this many
// deliberately-published channels is not a case worth designing for, and
// an unbounded list would let one request pull an arbitrary amount of
// writing (names) out of a host. Truncation is reported in the response
// so a caller can say "showing 64 of 90" instead of quietly lying.
const maxCatalogEntries = 64

// maxCatalogNameLen bounds an entry's name on receipt. Names are
// host-supplied text heading for a UI list.
const maxCatalogNameLen = 64

// catalogTimeout caps one catalog round trip. Same reasoning as
// probeTimeout: a store read under a user watching a spinner.
const catalogTimeout = 8 * time.Second

// ErrCatalogUnreachable means the host could not be reached or answered
// nothing — including a host running a build with no catalog at all,
// which is indistinguishable from silence and equally retryable.
var ErrCatalogUnreachable = errors.New("group: catalog: host did not answer")

// CatalogRequestMsg asks a visor what it publishes.
//
// Carries no filter and no cursor on purpose. A filter would invite
// probing ("do you host anything called X?") against unlisted groups, and
// the answer is bounded by maxCatalogEntries anyway.
type CatalogRequestMsg struct {
	Kind string    `json:"kind"`
	TS   time.Time `json:"ts"`
}

// CatalogEntryMsg is one published group on the wire.
//
// A subset of GroupDescriptor: enough to render a row and then join it,
// and nothing about the roster. Note it carries no member count — a
// listing is an invitation to join, not a report on who already did, and
// the count is the one field that turns a catalog into surveillance of a
// channel's popularity.
type CatalogEntryMsg struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	GroupKind Kind       `json:"group_kind"`
	Policy    JoinPolicy `json:"policy"`
	Port      uint16     `json:"port"`
	PoWBits   uint8      `json:"pow_bits,omitempty"`
	PriceHint string     `json:"price_hint,omitempty"`
}

// CatalogResponseMsg is the host's answer.
type CatalogResponseMsg struct {
	Kind    string            `json:"kind"`
	HostPK  cipher.PubKey     `json:"host_pk"`
	Entries []CatalogEntryMsg `json:"entries"`

	// Truncated reports that the host had more listed groups than it sent,
	// so a caller can say so rather than presenting a partial list as
	// complete. See maxCatalogEntries.
	Truncated bool `json:"truncated,omitempty"`
}

// CatalogEntry is one discovered group, host-side or caller-side.
type CatalogEntry struct {
	ID        string        `json:"id"`
	HostPK    cipher.PubKey `json:"host_pk"`
	Name      string        `json:"name"`
	Kind      Kind          `json:"kind"`
	Mode      Mode          `json:"mode"`
	Policy    JoinPolicy    `json:"policy"`
	Port      uint16        `json:"port"`
	PoWBits   uint8         `json:"pow_bits,omitempty"`
	PriceHint string        `json:"price_hint,omitempty"`

	// Address is the canonical skychat:// form, so a caller can hand a row
	// straight to the join path or render it as a QR code. Built by the
	// RECEIVER from HostPK + ID rather than trusted from the wire — a host
	// that could name its own address string could name someone else's.
	Address string `json:"address"`
}

// IsChannel reports whether this entry is a broadcast channel.
func (e CatalogEntry) IsChannel() bool { return e.Kind == KindChannel }

// Invite rebuilds the invite this entry is equivalent to, so a discovered
// row joins through exactly the same path as a pasted link. Carries no
// key, for the same reason GroupDescriptor.Invite does not.
func (e CatalogEntry) Invite() Invite {
	return Invite{
		ID:      e.ID,
		Name:    e.Name,
		OwnerPK: e.HostPK,
		Port:    e.Port,
		Mode:    e.Mode,
		Kind:    e.Kind,
		PoWBits: e.PoWBits,
	}
}

// ---------------------------------------------------------------------
// host side
// ---------------------------------------------------------------------

// Catalog returns what THIS visor publishes: every non-terminal record
// with Listed set, newest first, capped at maxCatalogEntries.
//
// Newest first because a catalog is a shop window and the thing just
// opened is the thing worth showing; the cap then drops the oldest rather
// than an arbitrary slice.
func (m *Manager) Catalog() ([]CatalogEntry, bool, error) {
	all, err := m.store.List()
	if err != nil {
		return nil, false, fmt.Errorf("group: Catalog: list: %w", err)
	}
	out := make([]CatalogEntry, 0, len(all))
	for _, r := range all {
		if !r.Listed || r.Status.IsTerminal() {
			continue
		}
		// Only a visor with roster authority can answer for a group: a
		// plain member advertising somebody else's channel would be
		// republishing a claim it cannot back, and its Port/policy view
		// may be stale in ways it cannot detect.
		if !r.IsAdmin(m.myPK) {
			continue
		}
		out = append(out, CatalogEntry{
			ID: r.ID, HostPK: r.OwnerPK, Name: r.Name,
			Kind: r.Kind, Mode: r.Mode, Policy: r.JoinPolicy(),
			Port: r.Port, PoWBits: r.JoinPoWRequired(),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	truncated := len(out) > maxCatalogEntries
	if truncated {
		out = out[:maxCatalogEntries]
	}
	return out, truncated, nil
}

// SetListed opts a group into or out of this visor's catalog.
//
// Admin-only, and deliberately NOT gossiped — see Record.Listed. It is a
// local hosting decision, so it writes the record directly rather than
// going through moderate(): there is no signed state for other members to
// converge on, and pretending otherwise would imply an admin can un-list
// a copy somebody else is advertising.
func (m *Manager) SetListed(id string, listed bool) (Record, error) {
	r, ok, err := m.store.Get(id)
	if err != nil {
		return Record{}, fmt.Errorf("group: SetListed: get: %w", err)
	}
	if !ok {
		return Record{}, fmt.Errorf("group: SetListed: no record for %s", id)
	}
	if !r.IsAdmin(m.myPK) {
		return Record{}, errors.New("group: SetListed: only admins can publish a group")
	}
	if r.Status.IsTerminal() {
		return Record{}, errors.New("group: SetListed: this group is over; nothing to publish")
	}
	if r.Listed == listed {
		return r, nil
	}
	r.Listed = listed
	if err := m.store.Put(r); err != nil {
		return Record{}, fmt.Errorf("group: SetListed: store: %w", err)
	}
	m.log.WithField("id", id).WithField("listed", listed).
		Info("group: catalog: publication changed")
	return r, nil
}

// handleCatalogRequest answers one catalog frame on the probe listener.
func (m *Manager) handleCatalogRequest(c net.Conn, asker cipher.PubKey) {
	entries, truncated, err := m.Catalog()
	resp := CatalogResponseMsg{Kind: frameKindCatalogResponse, HostPK: m.myPK}
	if err != nil {
		// An empty catalog is the honest answer to a failed store read:
		// there is nothing we can stand behind, and a listing we are not
		// sure of is worse than none.
		m.log.WithError(err).Debug("group: catalog: read failed; answering empty")
	} else {
		resp.Truncated = truncated
		resp.Entries = make([]CatalogEntryMsg, 0, len(entries))
		for _, e := range entries {
			resp.Entries = append(resp.Entries, CatalogEntryMsg{
				ID: e.ID, Name: truncate(e.Name, maxCatalogNameLen),
				GroupKind: e.Kind, Policy: e.Policy, Port: e.Port,
				PoWBits: e.PoWBits, PriceHint: truncate(e.PriceHint, maxPriceHintLen),
			})
		}
	}
	body, err := json.Marshal(&resp)
	if err != nil {
		m.log.WithError(err).Debug("group: catalog: encode response")
		return
	}
	if err := c.SetWriteDeadline(time.Now().Add(catalogTimeout)); err != nil {
		return
	}
	if err := message.WriteFrame(c, body); err != nil {
		m.log.WithError(err).Debug("group: catalog: write response")
		return
	}
	m.log.WithField("asker", asker.Hex()).WithField("entries", len(resp.Entries)).
		Debug("group: catalog: answered")
}

// ---------------------------------------------------------------------
// caller side
// ---------------------------------------------------------------------

// FetchCatalog asks host what it publishes.
//
// Every entry's Address is built here from the host PK we dialed plus the
// entry's own ID, never taken from the wire: the transport authenticates
// the host, so an address derived from it cannot point somewhere else,
// whereas a host-supplied string could advertise a channel it does not
// own. Same reason Kind is cross-checked against Mode on the join path.
func (m *Manager) FetchCatalog(ctx context.Context, host cipher.PubKey) ([]CatalogEntry, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req := CatalogRequestMsg{Kind: frameKindCatalogRequest, TS: time.Now().UTC()}
	body, err := json.Marshal(&req)
	if err != nil {
		return nil, false, fmt.Errorf("group: catalog: encode: %w", err)
	}

	frame, err := catalogRoundTrip(ctx, m.dmsgC, host, body)
	if err != nil {
		return nil, false, err
	}
	var resp CatalogResponseMsg
	if err := json.Unmarshal(frame, &resp); err != nil {
		return nil, false, fmt.Errorf("group: catalog: decode: %w", err)
	}
	if resp.Kind != frameKindCatalogResponse {
		return nil, false, fmt.Errorf("group: catalog: unexpected frame kind %q", resp.Kind)
	}

	out := make([]CatalogEntry, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		if e.ID == "" || e.Port == 0 {
			continue // unjoinable; drop rather than render a dead row
		}
		kind := e.GroupKind
		if !kind.IsValid() {
			kind = KindPublic // a host predating channels, or noise
		}
		mode := modeForKind(kind)
		policy := e.Policy
		if policy != JoinOpen && policy != JoinApproval {
			policy = policyForKind(kind)
		}
		out = append(out, CatalogEntry{
			ID:        e.ID,
			HostPK:    host,
			Name:      truncate(e.Name, maxCatalogNameLen),
			Kind:      kind,
			Mode:      mode,
			Policy:    policy,
			Port:      e.Port,
			PoWBits:   clampJoinPoWBits(e.PoWBits),
			PriceHint: truncate(e.PriceHint, maxPriceHintLen),
			Address:   catalogAddress(host, e.ID),
		})
		if len(out) >= maxCatalogEntries {
			// A host that ignores its own cap does not get to make us
			// render an unbounded list.
			return out, true, nil
		}
	}
	return out, resp.Truncated, nil
}

// catalogAddress renders the skychat:// group address for an entry.
//
// Spelled out here rather than importing pkg/skychat/address, which
// imports nothing from this package and must keep it that way — address
// is a leaf that names a group without knowing what one is. The grammar
// is fixed by that package's Scheme constant and its tests.
func catalogAddress(host cipher.PubKey, id string) string {
	return "skychat://" + host.Hex() + "/" + id
}

// catalogRoundTrip writes the request and reads the answer, skynet first
// then dmsg — the same preference order as every other skychat dial.
func catalogRoundTrip(ctx context.Context, dmsgC *dmsg.Client, host cipher.PubKey, body []byte) ([]byte, error) {
	skyAddr := appnet.Addr{Net: appnet.TypeSkynet, PubKey: host, Port: routing.Port(ProbePort)}
	if conn, dialErr := dialSkynetRelay(ctx, skyAddr); dialErr == nil {
		frame, rtErr := catalogExchange(conn, body)
		_ = conn.Close() //nolint:errcheck
		if rtErr == nil {
			return frame, nil
		}
	}
	if dmsgC == nil {
		return nil, ErrCatalogUnreachable
	}
	dialCtx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	stream, err := dmsgC.DialStream(dialCtx, dmsg.Addr{PK: host, Port: ProbePort})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCatalogUnreachable, err)
	}
	defer stream.Close() //nolint:errcheck
	return catalogExchange(stream, body)
}

func catalogExchange(c net.Conn, body []byte) ([]byte, error) {
	deadline := time.Now().Add(catalogTimeout)
	if err := c.SetWriteDeadline(deadline); err != nil {
		return nil, fmt.Errorf("group: catalog: set write deadline: %w", err)
	}
	if err := message.WriteFrame(c, body); err != nil {
		return nil, fmt.Errorf("group: catalog: write: %w", err)
	}
	if err := c.SetReadDeadline(deadline); err != nil {
		return nil, fmt.Errorf("group: catalog: set read deadline: %w", err)
	}
	frame, err := message.ReadFrame(c)
	if err != nil {
		// A host that takes the frame and says nothing is a build without
		// a catalog — reported as unreachable, which is both true and the
		// retryable answer a caller should act on.
		return nil, fmt.Errorf("%w: %v", ErrCatalogUnreachable, err)
	}
	return frame, nil
}
