package wasmhv

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/pbkdf2"
)

// fakeUI is a minimal Angular-build-shaped FS: an index.html that references a
// module chunk + a stylesheet (twice, like the real build), plus those files.
func fakeUI() fstest.MapFS {
	index := `<!doctype html><html><head><title>t</title>` +
		`<link rel="stylesheet" href="styles.abc.css" media="print" onload="this.media='all'">` +
		`<link rel="stylesheet" href="styles.abc.css">` +
		`<script src="main.def.js" type="module"></script>` +
		`</head><body><app-root></app-root></body></html>`
	return fstest.MapFS{
		"index.html":          {Data: []byte(index)},
		"main.def.js":         {Data: []byte("console.log('app'); var x='</script>';")},
		"styles.abc.css":      {Data: []byte("body{margin:0}")},
		"assets/i18n/en.json": {Data: []byte(`{"hello":"world"}`)},
		"assets/img/logo.png": {Data: []byte("\x89PNG\x00binary")},
	}
}

func TestGenerateStandalone_Structure(t *testing.T) {
	out, err := GenerateStandalone(fakeUI(),
		[]byte("globalThis.Go=function(){};"), // fake wasm_exec.js
		[]byte("\x00asm-fake-binary"),         // fake wasm
		[]byte("/*override*/"),                // fake override.js
		StandaloneConfig{
			Standalone: true,
			SeedPK:     "0371ab",
			SeedWS:     "ws://1.2.3.4/dmsg",
			Disc:       "dmsg://022e:80",
		})
	require.NoError(t, err)
	s := string(out)

	// No external script/style references survive (single-file).
	require.NotContains(t, s, `src="main.def.js"`, "script not inlined")
	require.NotContains(t, s, `href="styles.abc.css"`, "stylesheet not inlined")
	require.NotContains(t, s, "<link", "a <link> survived")

	// App chunk inlined, with </script> neutralized so it can't break out.
	require.Contains(t, s, "console.log('app')")
	require.NotContains(t, s, "var x='</script>'", "raw </script> would break the inline script")
	require.Contains(t, s, `<\/script`)

	// CSS inlined as <style>, deduped (only one copy despite two <link>s).
	require.Equal(t, 1, strings.Count(s, "body{margin:0}"), "stylesheet should be inlined once")

	// Config + wasm bootstrap + override all present, before the app chunk.
	require.Contains(t, s, "window.__SKYWIRE_HV__ = {")
	require.Contains(t, s, "standalone:true")
	require.Contains(t, s, `seedws:"ws://1.2.3.4/dmsg"`)
	require.Contains(t, s, "DecompressionStream")
	require.Contains(t, s, "globalThis.Go=function(){}")
	require.Contains(t, s, "/*override*/")
	require.Less(t, strings.Index(s, "/*override*/"), strings.Index(s, "console.log('app')"),
		"override.js must precede the Angular app chunk")

	// Runtime assets inlined into the served map with the right path + content
	// type, base64-encoded.
	require.Contains(t, s, "self.__SKYWIRE_ASSETS__={")
	require.Contains(t, s, `"/assets/i18n/en.json":{ct:"application/json",b:"`+
		base64.StdEncoding.EncodeToString([]byte(`{"hello":"world"}`))+`"}`)
	require.Contains(t, s, `"/assets/img/logo.png":{ct:"image/png",b:"`+
		base64.StdEncoding.EncodeToString([]byte("\x89PNG\x00binary"))+`"}`)
}

func TestGenerateStandalone_ViewerMode(t *testing.T) {
	out, err := GenerateStandalone(fakeUI(),
		[]byte("globalThis.Go=function(){};"), []byte("w"), []byte("o"),
		StandaloneConfig{HypervisorPK: "03abcd"})
	require.NoError(t, err)
	require.Contains(t, string(out), `pk:"03abcd"`)
	require.NotContains(t, string(out), "standalone:true")
}

// TestGenerateStandalone_VisorMode verifies the wasm-VISOR target emits CFG.visor
// (so override.js boots via skywireVisor + routes /api to the visor's hvApi) and
// injects the TinyGo getRandomData shim the key-generating visor build needs.
func TestGenerateStandalone_VisorMode(t *testing.T) {
	out, err := GenerateStandalone(fakeUI(),
		[]byte("globalThis.Go=function(){};"), // stand-in TinyGo wasm_exec.js
		[]byte("\x00asm-visor"), []byte("/*override*/"),
		StandaloneConfig{Visor: true, SeedPK: "0371ab", Disc: "dmsg://022e:80"})
	require.NoError(t, err)
	s := string(out)
	require.Contains(t, s, "visor:true")
	require.NotContains(t, s, "standalone:true")
	require.NotContains(t, s, `{pk:`, "visor mode has no remote hypervisor pk field")
	// The TinyGo getRandomData shim must be present so the wasm instantiates.
	require.Contains(t, s, `runtime.getRandomData`)
	require.Contains(t, s, "DecompressionStream")
	// Visor mode ships the browse/host overlay: the browse.js engine + the
	// launcher that mounts the panel.
	require.Contains(t, s, "globalThis.SkywireBrowse", "browse.js engine must be injected in visor mode")
	require.Contains(t, s, "SkywireBrowse.mountPanel", "the overlay launcher must be injected in visor mode")
}

// TestGenerateStandalone_NoShimForGoTarget confirms the non-visor (Go wasm) path
// does NOT inject the TinyGo getRandomData shim (Go's wasm_exec.js provides it).
func TestGenerateStandalone_NoShimForGoTarget(t *testing.T) {
	out, err := GenerateStandalone(fakeUI(),
		[]byte("globalThis.Go=function(){};"), []byte("w"), []byte("o"),
		StandaloneConfig{Standalone: true})
	require.NoError(t, err)
	require.NotContains(t, string(out), `runtime.getRandomData`)
}

// TestGenerateStandalone_EncryptedKey verifies the baked-in encsk decrypts with
// the exact PBKDF2-SHA256(200000) + AES-GCM scheme override.js resolveSK uses:
// base64([16-salt | 12-iv | ciphertext]).
func TestGenerateStandalone_EncryptedKey(t *testing.T) {
	const sk = "1de8a5e6c7cce88afb77b13279465fce2c6c3a29cfbdcde7c5b109348ca065e2"
	const pw = "correct horse battery staple"
	out, err := GenerateStandalone(fakeUI(),
		[]byte("globalThis.Go=function(){};"), []byte("w"), []byte("o"),
		StandaloneConfig{Standalone: true, SecretKey: sk, Password: pw})
	require.NoError(t, err)
	s := string(out)
	require.NotContains(t, s, sk, "plaintext secret key must NOT be embedded")
	require.NotContains(t, s, `{sk:`, "must use encsk, not a bare sk, when a password is set")
	require.NotContains(t, s, `,sk:`, "must use encsk, not a bare sk, when a password is set")
	require.Contains(t, s, `encsk:"`, "encrypted key must be embedded")

	// Extract encsk:"..." and decrypt it the way the browser would.
	i := strings.Index(s, `encsk:"`)
	require.GreaterOrEqual(t, i, 0)
	rest := s[i+len(`encsk:"`):]
	enc := rest[:strings.IndexByte(rest, '"')]

	raw, err := base64.StdEncoding.DecodeString(enc)
	require.NoError(t, err)
	require.Greater(t, len(raw), 28)
	salt, iv, ct := raw[:16], raw[16:28], raw[28:]
	key := pbkdf2.Key([]byte(pw), salt, 200000, 32, sha256.New)
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	pt, err := gcm.Open(nil, iv, ct, nil)
	require.NoError(t, err)
	require.Equal(t, sk, string(pt), "decrypted key must match the original")
}
