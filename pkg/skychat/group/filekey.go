// Package group pkg/skychat/group/filekey.go c4-app-chat
// per-file keys for group attachments.
//
// # Why attachments needed their own answer
//
// A group message body is sealed under the group key; a file attached to
// one was not sealed at all. The bytes ride an out-of-band xfer stream
// (encrypted in transit by the skywire transport, like everything else)
// and then sit on every member's disk in the clear, next to a chat
// history whose text is protected. Worse, the bytes are fetched by a
// backfill request keyed on nothing but the file id: whoever asks the
// holder for that id gets the file, so a member evicted yesterday — who
// still knows every id it saw — could keep pulling attachments long after
// losing the roster seat.
//
// Sealing the bytes under the group key fixes both halves at once: at
// rest they are ciphertext, and a re-send to someone outside the group
// hands over bytes they cannot open.
//
// # Why a derived key and not the group key
//
// The bulk crypto happens in the chat app (cmd/apps/skychat/commands),
// which reaches the group manager over the visor's RPC — a different
// process. Handing the raw group key across that boundary so the app can
// seal one attachment would put the key that opens the ENTIRE group,
// past and present, into a second address space for the sake of one file.
//
// So the visor hands over a key scoped to exactly one file:
//
//	fileKey = HKDF-SHA256(group key, info = "skychat-group-file-v1" | group id | file id)
//
// A leaked file key opens that attachment and nothing else — not the
// messages, not another attachment, not the same file in another group.
// The group key itself never leaves the visor.
//
// Opening takes the same derivation over every key in Record.KeyRing, so
// an attachment shared before a rotation still opens afterwards, exactly
// as message history does. A joiner that only holds the current key
// cannot open attachments from before it arrived — the same boundary
// admission already draws for message history.
package group

import (
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// fileKeyInfoPrefix domain-separates attachment keys from every other use
// of the group key. Versioned: changing the derivation means changing this
// string, so old and new never silently produce different keys under one
// name.
const fileKeyInfoPrefix = "skychat-group-file-v1"

// deriveFileKey returns the 32-byte AEAD key for one attachment in one
// group, derived from a group key.
//
// Both ids are folded in, not just the file id: file ids are unique per
// send, but binding the group too means a key handed out for a file in one
// group is inert in another even if an id ever repeated.
func deriveFileKey(groupKey []byte, groupID, fileID string) ([]byte, error) {
	if len(groupKey) != 32 {
		return nil, fmt.Errorf("group: file key: group key must be 32 bytes, got %d", len(groupKey))
	}
	if fileID == "" {
		return nil, fmt.Errorf("group: file key: empty file id")
	}
	info := make([]byte, 0, len(fileKeyInfoPrefix)+1+len(groupID)+1+len(fileID))
	info = append(info, fileKeyInfoPrefix...)
	info = append(info, '|')
	info = append(info, groupID...)
	info = append(info, '|')
	info = append(info, fileID...)

	out := make([]byte, 32)
	r := hkdf.New(sha256.New, groupKey, nil, info)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, fmt.Errorf("group: file key: hkdf: %w", err)
	}
	return out, nil
}

// FileKeys returns the key to seal a new attachment with and every key
// that may open an existing one, for the given file id.
//
// seal is nil for a plaintext group — a public group hands its key to
// anyone who asks, so sealing an attachment with it would protect nothing
// and would only add a way for the file to become unreadable. The caller
// sends such attachments as-is.
//
// open is ordered current-key-first, exactly like Record.DecryptionKeys,
// so the common case (an attachment shared under the current epoch) opens
// on the first try and the ring is walked only for older files.
func (r Record) FileKeys(fileID string) (seal []byte, open [][]byte, err error) {
	if !r.Encrypted() {
		return nil, nil, nil
	}
	keys := r.DecryptionKeys()
	if len(keys) == 0 {
		return nil, nil, errNoGroupKey
	}
	open = make([][]byte, 0, len(keys))
	for _, k := range keys {
		fk, dErr := deriveFileKey(k, r.ID, fileID)
		if dErr != nil {
			// A malformed ring entry must not take the whole set with it:
			// the key we actually need is probably one of the others.
			continue
		}
		open = append(open, fk)
	}
	if len(open) == 0 {
		return nil, nil, errNoGroupKey
	}
	// Sealing derives from the CURRENT key explicitly rather than taking
	// open[0]. They are the same value today — DecryptionKeys puts the
	// current key first — but the positional form would silently start
	// sealing under a retired key the day that ordering changed or the
	// current key's derivation was the one that got skipped above.
	seal, err = deriveFileKey(r.AESKey, r.ID, fileID)
	if err != nil {
		return nil, nil, err
	}
	return seal, open, nil
}

// FileKeys resolves the attachment keys for a group this visor is in. A
// plaintext group yields a nil seal key and no error (see Record.FileKeys).
func (m *Manager) FileKeys(groupID, fileID string) (seal []byte, open [][]byte, epoch uint64, err error) {
	r, ok, err := m.store.Get(groupID)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("group: file keys: %w", err)
	}
	if !ok {
		return nil, nil, 0, fmt.Errorf("group: file keys: no record for %s", groupID)
	}
	seal, open, err = r.FileKeys(fileID)
	if err != nil {
		return nil, nil, 0, err
	}
	return seal, open, r.KeyEpoch, nil
}

// FileKeys is the live-session form, for the standalone chat app that owns
// its group session in-process instead of reaching a visor over RPC.
func (s *Session) FileKeys(fileID string) (seal []byte, open [][]byte, err error) {
	s.membersMu.RLock()
	defer s.membersMu.RUnlock()
	return s.cfg.Record.FileKeys(fileID)
}
