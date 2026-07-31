// Package commands cmd/apps/skychat/commands/filecrypt_keys.go c4-app-chat
//
// Where the chat app gets the key for a group attachment, and the two
// wrappers every caller actually uses: seal-this-file-for-that-group, and
// open-whatever-this-file-turns-out-to-be.
//
// Two sources, because the app runs in two shapes:
//
//   - Visor-managed groups (the normal one): the group record lives in the
//     visor, so the key comes back over the same RPC the rest of the group
//     surface uses. The visor derives a key scoped to ONE file id and
//     returns that — never the group key. See pkg/visor.Visor.GroupFileKey.
//   - Standalone --cxo-group: the app owns the group session in-process,
//     so it derives the same key from the session directly. Same
//     derivation, no RPC.
//
// Keys are not cached. A cache would have to be invalidated on every
// rotation, eviction and re-join to avoid sealing under a key the group
// has retired — and the derivation is one HKDF over a key the visor
// already has in memory, so there is nothing to save.
package commands

import (
	"fmt"
	"path/filepath"

	"github.com/skycoin/skywire/pkg/skychat/xfer"
	"github.com/skycoin/skywire/pkg/visor"
)

// groupFileKeys resolves the seal + open keys for one attachment.
//
// A nil seal key with a nil error is the plaintext-group answer: a public
// group hands its key to whoever asks, so sealing with it would protect
// nothing. The caller sends and serves such files unchanged.
func groupFileKeys(groupID, fileID string) (seal []byte, open [][]byte, err error) {
	if groupID == "" || fileID == "" {
		return nil, nil, fmt.Errorf("skychat: attachment keys: group id and file id required")
	}
	// Standalone first: when the app owns the session there is no visor to
	// ask, and asking would fail rather than fall through.
	if cxoGroupSess != nil && groupID == cxoGroup {
		return cxoGroupSess.FileKeys(fileID)
	}
	var res visor.GroupFileKeyResult
	if rErr := pairRPCCall("GroupFileKey", func(c visor.API) error {
		var cErr error
		res, cErr = c.GroupFileKey(visor.GroupFileKeyArgs{ID: groupID, FileID: fileID})
		return cErr
	}); rErr != nil {
		return nil, nil, fmt.Errorf("skychat: attachment keys: %w", rErr)
	}
	return res.Seal, res.Open, nil
}

// sealGroupAttachment writes the sealed container for srcPath to dstPath.
//
// Reports whether it sealed: a plaintext group has no key, so the caller
// falls back to copying the file as it is. Any other failure is returned —
// silently shipping a group attachment in the clear would be the exact bug
// this file exists to fix.
func sealGroupAttachment(srcPath, dstPath, groupID, fileID, name string) (bool, error) {
	seal, _, err := groupFileKeys(groupID, fileID)
	if err != nil {
		return false, err
	}
	if len(seal) == 0 {
		return false, nil // plaintext group
	}
	if err := sealFileTo(srcPath, dstPath, groupID, fileID, name, seal); err != nil {
		return false, err
	}
	return true, nil
}

// storeGroupAttachment writes the copy this visor will serve and re-send
// for a group attachment: sealed for an encrypted group, a plain copy for
// a public one. The destination is the id-named served copy the backfill
// path looks up (sentCopyName).
//
// Errors are the caller's to propagate, not to log and continue past. The
// served copy is what every other member will pull, so "sealing failed,
// carry on" would publish a file reference whose bytes are readable by
// anyone who asks for them.
func storeGroupAttachment(srcPath, groupID, fileID, name string) error {
	dir, err := downloadsDir()
	if err != nil {
		return err
	}
	dst := filepath.Join(dir, sentCopyName(xfer.Offer{ID: fileID, Name: name}))
	sealed, err := sealGroupAttachment(srcPath, dst, groupID, fileID, name)
	if err != nil {
		return fmt.Errorf("skychat: sealing %q for the group: %w", name, err)
	}
	if sealed {
		return nil
	}
	// Public group: no key exists, so the copy is stored as it is.
	if err := copyFile(srcPath, dst); err != nil {
		return fmt.Errorf("skychat: group attachment copy: %w", err)
	}
	return nil
}

// openAttachment returns a plaintext, seekable view of a file in the
// downloads dir, whatever it turns out to be.
//
// Three outcomes, and callers care about all three:
//
//   - (reader, nil) — a sealed group attachment we can open.
//   - (nil, errNotSealed) — an ordinary file: a DM attachment, a
//     public-group one, or one from a build that predates sealing. Serve
//     it straight from disk.
//   - (nil, other) — sealed, but no key we hold opens it. That is the
//     honest answer for an attachment from a group we have left or an
//     epoch we never received; it must NOT be served as bytes.
func openAttachment(path string) (*sealedFileReader, error) {
	hdr, err := sealedFileHeader(path)
	if err != nil {
		return nil, err // errNotSealed, or a genuinely broken header
	}
	_, open, err := groupFileKeys(hdr.GroupID, hdr.FileID)
	if err != nil {
		return nil, fmt.Errorf("skychat: attachment %s: %w", filepath.Base(path), err)
	}
	if len(open) == 0 {
		return nil, fmt.Errorf("skychat: attachment %s: group holds no key", filepath.Base(path))
	}
	return openSealedFile(path, open)
}
