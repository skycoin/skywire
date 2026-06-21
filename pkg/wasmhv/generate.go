package wasmhv

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

// generate.go assembles a self-contained, serverless hypervisor.html from the
// built Angular UI + the WASM dmsg client + override.js. The output is a single
// file with NO external references: the Angular chunk scripts and stylesheets
// are inlined, the WASM binary is gzip+base64 embedded (decompressed in-browser
// via DecompressionStream), override.js is injected, and the dmsg config +
// (optionally password-encrypted) secret key are baked into window.__SKYWIRE_HV__.
//
// Opening the file from file:// boots the WASM dmsg client and runs the
// hypervisor UI serverlessly — see docs/design/unified-wasm-hypervisor-ui.md.
// The plaintext key never touches disk: with a password, only the AES-GCM
// ciphertext is embedded and the page prompts to decrypt it (override.js
// resolveSK). NEVER serve a generated (key-bearing) file from a domain.
//
// Known gap (tracked): runtime Angular assets (i18n JSON, flag/logo images under
// /assets) are not yet inlined, so a generated file shows untranslated strings +
// broken images until the asset-FS inlining lands. Scripts/styles/app logic work.

// StandaloneConfig is the dmsg + identity config baked into a generated file.
type StandaloneConfig struct {
	// Standalone, when true, emits CFG.standalone (this tab IS the hypervisor —
	// visors dial in). Otherwise HypervisorPK must be set (viewer mode — dial a
	// remote hypervisor).
	Standalone   bool
	HypervisorPK string // CFG.pk for viewer mode

	// dmsg bootstrap: a seed server (PK + ws:// URL) the browser connects to
	// first, then upgrades discovery to run over dmsg.
	SeedPK string
	SeedWS string
	Disc   string

	// SecretKey is the hypervisor identity secret key (hex). When Password is
	// set it is AES-GCM encrypted into CFG.encsk (the plaintext is never
	// embedded); without a password it is embedded as CFG.sk (testing only);
	// empty yields an ephemeral identity.
	SecretKey string
	Password  string
}

var (
	reScriptSrc = regexp.MustCompile(`(?s)<script\b([^>]*?)\bsrc="([^"]+)"([^>]*?)>\s*</script>`)
	reLinkCSS   = regexp.MustCompile(`(?s)<link\b[^>]*?\brel="stylesheet"[^>]*?\bhref="([^"]+)"[^>]*?>`)
	reHeadOpen  = regexp.MustCompile(`(?i)<head\b[^>]*>`)
)

// GenerateStandalone assembles the single-file hypervisor.html. uiFS is the
// built Angular FS (must contain index.html and the referenced chunk JS/CSS);
// wasmExecJS is Go's lib/wasm/wasm_exec.js; wasm is the raw js/wasm dmsg-client
// binary; overrideJS is pkg/wasmhv/override.js.
func GenerateStandalone(uiFS fs.FS, wasmExecJS, wasm, overrideJS []byte, cfg StandaloneConfig) ([]byte, error) {
	indexB, err := fs.ReadFile(uiFS, "index.html")
	if err != nil {
		return nil, fmt.Errorf("read index.html: %w", err)
	}
	html := string(indexB)

	// Inline every <script src="X"> (preserving its other attrs, e.g.
	// type="module", so the deferred/ordered execution semantics are kept) with
	// the chunk's contents.
	var inlineErr error
	html = reScriptSrc.ReplaceAllStringFunc(html, func(tag string) string {
		m := reScriptSrc.FindStringSubmatch(tag)
		src := strings.TrimPrefix(m[2], "/")
		body, rErr := fs.ReadFile(uiFS, src)
		if rErr != nil {
			inlineErr = fmt.Errorf("inline script %q: %w", src, rErr)
			return tag
		}
		return "<script" + m[1] + m[3] + ">" + jsSafe(body) + "</script>"
	})
	if inlineErr != nil {
		return nil, inlineErr
	}

	// Inline every <link rel="stylesheet" href="X"> as a <style>, deduping by
	// href (Angular emits the same stylesheet twice: an async-load <link> + a
	// <noscript> fallback).
	seenCSS := map[string]bool{}
	html = reLinkCSS.ReplaceAllStringFunc(html, func(tag string) string {
		m := reLinkCSS.FindStringSubmatch(tag)
		href := strings.TrimPrefix(m[1], "/")
		if seenCSS[href] {
			return ""
		}
		seenCSS[href] = true
		body, rErr := fs.ReadFile(uiFS, href)
		if rErr != nil {
			inlineErr = fmt.Errorf("inline stylesheet %q: %w", href, rErr)
			return tag
		}
		return "<style>" + string(body) + "</style>"
	})
	if inlineErr != nil {
		return nil, inlineErr
	}

	// Build the head block: dmsg config, then the WASM bootstrap (wasm_exec.js +
	// gzip-base64 binary decompressed via DecompressionStream + instantiate),
	// then override.js. All classic <script>s in <head>, so they run before the
	// deferred Angular module scripts — override.js must shim XHR/fetch before
	// Angular's HttpClient is constructed.
	cfgJS, err := configJS(cfg)
	if err != nil {
		return nil, err
	}
	wasmJS, err := wasmBootstrap(wasm)
	if err != nil {
		return nil, err
	}
	assetsJS, err := assetMapJS(uiFS)
	if err != nil {
		return nil, err
	}
	head := "\n<script>" + cfgJS + "</script>\n" +
		"<script>" + assetsJS + "</script>\n" +
		"<script>" + jsSafe(wasmExecJS) + "</script>\n" +
		"<script>" + wasmJS + "</script>\n" +
		"<script>" + jsSafe(overrideJS) + "</script>\n"

	loc := reHeadOpen.FindStringIndex(html)
	if loc == nil {
		return nil, fmt.Errorf("no <head> in index.html")
	}
	html = html[:loc[1]] + head + html[loc[1]:]

	return []byte(html), nil
}

// configJS renders window.__SKYWIRE_HV__ from cfg, encrypting the secret key
// when a password is set.
func configJS(cfg StandaloneConfig) (string, error) {
	fields := []string{}
	add := func(k, v string) {
		if v != "" {
			fields = append(fields, fmt.Sprintf("%s:%s", k, jsString(v)))
		}
	}
	if cfg.Standalone {
		fields = append(fields, "standalone:true")
	} else {
		add("pk", cfg.HypervisorPK)
	}
	add("seedpk", cfg.SeedPK)
	add("seedws", cfg.SeedWS)
	add("disc", cfg.Disc)

	switch {
	case cfg.SecretKey != "" && cfg.Password != "":
		enc, err := encryptKey(cfg.SecretKey, cfg.Password)
		if err != nil {
			return "", err
		}
		add("encsk", enc)
	case cfg.SecretKey != "":
		add("sk", cfg.SecretKey)
	}
	return "window.__SKYWIRE_HV__ = {" + strings.Join(fields, ",") + "};", nil
}

// encryptKey AES-GCM-encrypts the secret-key hex string under a
// PBKDF2-SHA256(200000) key, returning base64([16-salt | 12-iv | ciphertext]) —
// the exact format override.js resolveSK() decrypts.
func encryptKey(secretKeyHex, password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	iv := make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	key := pbkdf2.Key([]byte(password), salt, 200000, 32, sha256.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, iv, []byte(secretKeyHex), nil)
	out := append(append(append([]byte{}, salt...), iv...), ct...)
	return base64.StdEncoding.EncodeToString(out), nil
}

// wasmBootstrap gzips the wasm, base64s it, and emits the JS that decompresses
// it in-browser (DecompressionStream) and instantiates + runs it. Go's wasm sets
// globalThis.skywireDmsg and then blocks, so go.run is fired (not awaited).
func wasmBootstrap(wasm []byte) (string, error) {
	var gz bytes.Buffer
	w, err := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := w.Write(wasm); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	b64 := base64.StdEncoding.EncodeToString(gz.Bytes())
	return `(async function(){
  var b64 = "` + b64 + `";
  var bin = Uint8Array.from(atob(b64), function(c){ return c.charCodeAt(0); });
  var buf = await new Response(new Blob([bin]).stream().pipeThrough(new DecompressionStream("gzip"))).arrayBuffer();
  var go = new Go();
  var res = await WebAssembly.instantiate(buf, go.importObject);
  go.run(res.instance);
})();`, nil
}

// assetMapJS inlines the UI's runtime assets (everything under assets/ — i18n
// JSON, images, fonts) into a JS object the standalone file serves locally:
//
//	self.__SKYWIRE_ASSETS__ = {"/assets/i18n/en.json":{ct:"application/json",b:"<base64>"}, ...}
//
// override.js's serve() returns these directly (no backend, no dmsg round-trip),
// so a file:// hypervisor.html shows translations + images. Empty string when the
// FS has no assets/ dir. The base64 inflates the file ~33% over the raw asset
// size (gzip / excluding the country-flag PNGs are future size levers).
func assetMapJS(uiFS fs.FS) (string, error) {
	if _, err := fs.Stat(uiFS, "assets"); err != nil {
		return "self.__SKYWIRE_ASSETS__={};", nil //nolint:nilerr // no assets dir → empty map
	}
	var b strings.Builder
	b.WriteString("self.__SKYWIRE_ASSETS__={")
	first := true
	err := fs.WalkDir(uiFS, "assets", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		data, rErr := fs.ReadFile(uiFS, path)
		if rErr != nil {
			return rErr
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteString(jsString("/" + path))
		b.WriteString(":{ct:")
		b.WriteString(jsString(assetContentType(path)))
		b.WriteString(",b:")
		b.WriteString(jsString(base64.StdEncoding.EncodeToString(data)))
		b.WriteByte('}')
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("inline assets: %w", err)
	}
	b.WriteString("};")
	return b.String(), nil
}

// assetContentType maps a file extension to a MIME type for the inlined assets.
func assetContentType(path string) string {
	dot := strings.LastIndexByte(path, '.')
	if dot < 0 {
		return "application/octet-stream"
	}
	switch strings.ToLower(path[dot+1:]) {
	case "json":
		return "application/json"
	case "png":
		return "image/png"
	case "svg":
		return "image/svg+xml"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "css":
		return "text/css"
	case "js":
		return "text/javascript"
	case "woff2":
		return "font/woff2"
	case "woff":
		return "font/woff"
	case "ttf":
		return "font/ttf"
	case "eot":
		return "application/vnd.ms-fontobject"
	default:
		return "application/octet-stream"
	}
}

// jsSafe prevents an inlined script's bytes from prematurely closing the
// enclosing <script> element (the standard </script -> <\/script transform;
// "</script" is never a valid JS token outside a string/regex, where the escape
// is equivalent).
func jsSafe(b []byte) string {
	return strings.ReplaceAll(string(b), "</script", `<\/script`)
}

// jsString renders s as a JSON-ish double-quoted JS string literal (the values
// here are PKs / URLs / base64, so only quote + backslash need escaping).
func jsString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}
