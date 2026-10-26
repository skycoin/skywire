// Package commands cmd/apps/skychat/commands/identity.go c4-app-chat
//
// Which files an identity's state lives in. The app's work dir survives a
// change of the visor's key — the operator changed identity, not machine —
// so state that is really the identity's (the address book, who is blocked,
// the conversation history) has to name its owner or it leaks across the
// change and the new identity starts with the old one's friends.
package commands

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/skycoin/skywire/pkg/skyenv"
)

// stateDir is the work dir every store file lives under.
func stateDir() string {
	workDir := ""
	if appCl != nil {
		workDir = appCl.Config().ProcWorkDir
	}
	if workDir == "" {
		workDir = skyenv.LocalPath
	}
	return workDir
}

// identityScope is the visor's public key hex — the identity the state
// belongs to. Empty when there is none to scope by: --standalone has no
// visor handshake and keeps the shared files.
func identityScope() string {
	if appCl == nil {
		return ""
	}
	return appCl.Config().VisorPK.Hex()
}

// identityStatePath picks the file for name under dir, scoped to the
// identity. The unscoped file from before identities existed is adopted by
// the first identity to run after the upgrade — a deployment's existing book
// belongs to whoever upgrades it — and every later identity starts clean,
// which is what changing identity means. An adoption that cannot be renamed
// leaves the new identity a fresh file rather than someone else's.
func identityStatePath(dir, scope, name string) string {
	if scope == "" {
		return filepath.Join(dir, name)
	}
	scoped := filepath.Join(dir, scopedName(name, scope))
	if info, err := os.Stat(scoped); err == nil && !info.IsDir() {
		return scoped
	}
	legacy := filepath.Join(dir, name)
	if info, err := os.Stat(legacy); err == nil && !info.IsDir() {
		if err := os.Rename(legacy, scoped); err != nil && appLog != nil {
			appLog("state: adopt %s for %s: %v", name, scope, err)
		}
	}
	return scoped
}

// scopedName puts the identity before the extension:
// skychat-contacts.json → skychat-contacts-<pk>.json.
func scopedName(name, scope string) string {
	if ext := filepath.Ext(name); ext != "" {
		return strings.TrimSuffix(name, ext) + "-" + scope + ext
	}
	return name + "-" + scope
}
