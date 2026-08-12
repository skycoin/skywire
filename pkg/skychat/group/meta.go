// Package group pkg/skychat/group/meta.go c4-app-chat
// display metadata after creation: renaming a group, giving it a picture.
//
// # Ownership model — deliberately not gossip
//
// Name and avatar are display metadata with a single natural author: the
// founding visor. Making them a fifth signed gossip family (alongside
// roster/admin/mod/keys) would buy multi-admin authorship at the price of
// canonical-bytes changes, sealing, replay guards and a parking lot — for
// data that has no integrity stake: nothing admits, decrypts, or moderates
// based on a name. So the founder's record is simply THE record, and
// everyone else converges on it by asking:
//
//   - the founder edits locally (Rename / SetAvatar below);
//   - new joiners get the current name in the admission response, and the
//     picture on their first refresh (see JoinResponseMsg.Avatar for why
//     not both);
//   - members re-learn both through RefreshMeta, which rides the existing
//     describe probe — the UI fires it when a group is opened.
//
// The cost is honesty about staleness: a member that never asks keeps the
// old name, exactly as a saved contact keeps the name it was saved under.
package group

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/skycoin/skywire/pkg/skychat/profile"
)

// metaMaxNameLen bounds a group name in bytes. The same bound the catalog
// already enforces on receipt (maxCatalogNameLen) — a longer name would
// only ever display truncated, so refuse it at the source instead.
const metaMaxNameLen = maxCatalogNameLen

// Rename changes a group's display name.
//
// Founder-only: members hold mirrors of the founder's record, and a mirror
// that renames itself is overwritten by its next RefreshMeta. Refusing the
// write is kinder than letting it silently un-happen.
func (m *Manager) Rename(id, name string) (Record, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Record{}, errors.New("group: rename: a group needs a name")
	}
	if len(name) > metaMaxNameLen {
		return Record{}, fmt.Errorf("group: rename: name too long (max %d bytes)", metaMaxNameLen)
	}
	r, err := m.metaRecord(id, "rename")
	if err != nil {
		return Record{}, err
	}
	if r.Name == name {
		return r, nil
	}
	r.Name = name
	if err := m.store.Put(r); err != nil {
		return Record{}, fmt.Errorf("group: rename: store: %w", err)
	}
	m.log.WithField("id", id).WithField("name", name).Info("group: renamed")
	return r, nil
}

// SetAvatar sets or clears (empty bytes) a group's picture. Founder-only,
// same reasoning as Rename. The bytes are validated as a real image within
// the profile caps regardless of caller — this is the last gate before the
// store, and what is in the store goes out on the wire.
func (m *Manager) SetAvatar(id string, avatar []byte) (Record, error) {
	var mime string
	if len(avatar) > 0 {
		var err error
		if mime, err = profile.ValidateAvatar(avatar); err != nil {
			return Record{}, fmt.Errorf("group: set avatar: %w", err)
		}
	}
	r, err := m.metaRecord(id, "set avatar")
	if err != nil {
		return Record{}, err
	}
	if bytes.Equal(r.Avatar, avatar) && r.AvatarMime == mime {
		return r, nil
	}
	r.Avatar = append([]byte(nil), avatar...)
	r.AvatarMime = mime
	if err := m.store.Put(r); err != nil {
		return Record{}, fmt.Errorf("group: set avatar: store: %w", err)
	}
	m.log.WithField("id", id).WithField("bytes", len(avatar)).Info("group: avatar changed")
	return r, nil
}

// metaRecord loads a group and applies the gates shared by both edits.
func (m *Manager) metaRecord(id, op string) (Record, error) {
	r, ok, err := m.store.Get(id)
	if err != nil {
		return Record{}, fmt.Errorf("group: %s: get: %w", op, err)
	}
	if !ok {
		return Record{}, fmt.Errorf("group: %s: no record for %s", op, id)
	}
	if r.OwnerPK != m.myPK {
		return Record{}, fmt.Errorf("group: %s: only the founding visor can change this — members take name and picture from it", op)
	}
	if r.Status.IsTerminal() {
		return Record{}, fmt.Errorf("group: %s: this group is over", op)
	}
	return r, nil
}

// RefreshMeta re-reads a member-side record's display metadata from the
// founding visor and persists what changed, reporting whether anything did.
//
// Best-effort by design: an unreachable founder means nothing changed, not
// failure — the caller fires this when a group is opened, and "the founder
// is asleep" must not turn opening a chat into an error. Only local
// problems (no record, a store write failing) surface as errors. On the
// founder's own visor and on terminated records it is a no-op.
func (m *Manager) RefreshMeta(ctx context.Context, id string) (Record, bool, error) {
	r, ok, err := m.store.Get(id)
	if err != nil {
		return Record{}, false, fmt.Errorf("group: refresh meta: get: %w", err)
	}
	if !ok {
		return Record{}, false, fmt.Errorf("group: refresh meta: no record for %s", id)
	}
	if r.Status.IsTerminal() || r.OwnerPK == m.myPK || m.dmsgC == nil {
		return r, false, nil
	}
	// Straight to the wire, NOT ProbeGroup: its local shortcut would hand
	// back the very record being refreshed.
	resp, err := sendProbe(ctx, m.dmsgC, r.OwnerPK, id, m.myPK)
	if err != nil {
		m.log.WithError(err).WithField("id", id).Debug("group: refresh meta: founder unreachable")
		return r, false, nil
	}
	if resp.Status != JoinStatusInfo {
		// Banned, dropped, or an older founder build — none of these are
		// this path's business to act on.
		return r, false, nil
	}
	changed := false
	if name := truncate(strings.TrimSpace(resp.Name), metaMaxNameLen); name != "" && name != r.Name {
		r.Name = name
		changed = true
	}
	// The founder having no picture clears ours: the picture only ever
	// came from there, so absence is an answer, not missing data.
	avatar, mime := sanitizeAvatar(resp.Avatar)
	if !bytes.Equal(avatar, r.Avatar) || mime != r.AvatarMime {
		r.Avatar, r.AvatarMime = avatar, mime
		changed = true
	}
	if !changed {
		return r, false, nil
	}
	if err := m.store.Put(r); err != nil {
		return Record{}, false, fmt.Errorf("group: refresh meta: store: %w", err)
	}
	m.log.WithField("id", id).WithField("name", r.Name).
		Info("group: display metadata refreshed from founder")
	return r, true, nil
}

// sanitizeAvatar bounds untrusted avatar bytes off the wire: a real image
// within the profile caps, or nothing. The MIME is always the detected
// one — a peer's declared type is never stored.
func sanitizeAvatar(b []byte) ([]byte, string) {
	if len(b) == 0 {
		return nil, ""
	}
	mime, err := profile.ValidateAvatar(b)
	if err != nil {
		return nil, ""
	}
	return append([]byte(nil), b...), mime
}
