// Package visor pkg/visor/voice_ringback.go c3-vis-core
//
// Keeps this visor's ringback tone — what a caller hears while it rings —
// across restarts. The call manager holds it in memory; this is the copy on
// disk, beside the rest of the visor's local state.
package visor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/skycoin/skywire/pkg/logging"
	skycall "github.com/skycoin/skywire/pkg/skychat/call"
)

// voiceRingbackFile holds the tone's bytes; its type is kept in a file of the
// same name plus voiceRingbackMimeExt, since nothing in the bytes says it
// reliably.
const (
	voiceRingbackFile    = "voice-ringback"
	voiceRingbackMimeExt = ".mime"
)

// saveRingback writes the tone under localPath, or removes it when data is
// empty. A visor with no local path (a browser visor) keeps it in memory only.
func saveRingback(localPath string, data []byte, mime string) error {
	if localPath == "" {
		return nil
	}
	path := filepath.Join(localPath, voiceRingbackFile)
	if len(data) == 0 {
		for _, p := range []string{path, path + voiceRingbackMimeExt} {
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("voice: remove ringback tone: %w", err)
			}
		}
		return nil
	}
	if err := os.MkdirAll(localPath, 0o700); err != nil {
		return fmt.Errorf("voice: ringback tone dir: %w", err)
	}
	// The type first, then the bytes: a crash between the two leaves the
	// old tone under the new type at worst, which the manager re-validates
	// on load, rather than a tone with no type at all.
	if err := writeFileAtomic(path+voiceRingbackMimeExt, []byte(mime)); err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

// writeFileAtomic replaces path with data via a rename, so a reader never sees
// half a file.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("voice: write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("voice: write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// loadRingback hands the saved tone, if any, to the call manager.
func loadRingback(localPath string, mgr *skycall.Manager, log *logging.Logger) {
	if localPath == "" || mgr == nil {
		return
	}
	path := filepath.Join(localPath, voiceRingbackFile)
	data, err := os.ReadFile(path) //nolint:gosec // our own file under the visor's local path
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.WithError(err).Warn("voice: cannot read the ringback tone; callers hear the plain ring")
		}
		return
	}
	mime, err := os.ReadFile(path + voiceRingbackMimeExt) //nolint:gosec // as above
	if err != nil {
		log.WithError(err).Warn("voice: ringback tone has no type; callers hear the plain ring")
		return
	}
	if err := mgr.SetRingback(data, strings.TrimSpace(string(mime))); err != nil {
		log.WithError(err).Warn("voice: saved ringback tone rejected; callers hear the plain ring")
		return
	}
	log.WithField("bytes", len(data)).Debug("voice: ringback tone loaded")
}
