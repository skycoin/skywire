// Package visor pkg/visor/servedversion.go c3-vis-core
package visor

import (
	"github.com/skycoin/skywire/pkg/wasmhv/execwasm"

	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sync"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/wasmhv"
)

// servedVersionToken is the placeholder a pre-rendered page carries where its
// served-build fingerprint goes; renderServedVersion fills it per request. The
// pages are rendered once at startup, but the fingerprint is not a startup-time
// constant: it folds in the skywire command module served from disk, which is
// rebuilt in place while the server runs.
const servedVersionToken = "__SKYWIRE_SERVED_VERSION__"

// execWasmStamp fingerprints the skywire command module served at
// /skywire.wasm from disk — size + mtime, stat'd on every call. The file is
// ~170 MB and rebuilt in place, so hashing its content per poll is out, and a
// hash taken once at startup would miss every rebuild, which is exactly what
// the fingerprint exists to catch. Empty when there is no such file.
func execWasmStamp(path string) string {
	if path == "" {
		return ""
	}
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	h := sha256.New()
	fmt.Fprintf(h, "%d:%d", fi.Size(), fi.ModTime().UnixNano())
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// deskAssetsStamp fingerprints the desk client the host binary embeds and
// serves itself (desk-boot, the browse bundle, the exec worker) plus the
// binary's version, once. A rebuilt host that changes any of them changes the
// served build; one that changes none of them does not, and an open desk is
// not reloaded for nothing.
var deskAssetsStamp = sync.OnceValue(func() string {
	h := sha256.New()
	h.Write(wasmhv.DeskBootJS())
	h.Write(wasmhv.BrowseJS)
	h.Write(wasmhv.ExecWorkerJS())
	h.Write(wasmhv.AutoUpdateJS)
	h.Write([]byte(buildinfo.Version()))
	return hex.EncodeToString(h.Sum(nil))[:16]
})

// servedVersion is the fingerprint a desk page boots with and polls for: the
// served build plus the command module's stamp when one is served. It changes
// iff a reload would load something different.
func servedVersion(build, execWasmPath string) string {
	if s := execWasmStamp(execWasmPath); s != "" {
		return build + "-" + s
	}
	if execWasmPath == "" {
		if s := execwasm.Stamp(); s != "" {
			return build + "-" + s
		}
	}
	return build
}

// renderServedVersion fills a pre-rendered page's servedVersionToken with the
// current fingerprint. The token is hex/dash-free of quoting hazards on both
// sides, so it sits inside a JS string literal in the page.
func renderServedVersion(page []byte, version string) []byte {
	return bytes.ReplaceAll(page, []byte(servedVersionToken), []byte(version))
}
