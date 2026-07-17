//go:build tinygo && !(js && wasm)

package transport

// ARClient returns the address resolver client as an untyped value. Under TinyGo
// addrresolver is excluded from the build (it pulls net/http), so there is no
// concrete return type to expose; callers that need the resolver are themselves
// native-only.
func (tm *Manager) ARClient() any {
	return tm.arClient
}
