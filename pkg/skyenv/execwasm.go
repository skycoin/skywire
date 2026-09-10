// Package skyenv pkg/skyenv/execwasm.go c3-env
package skyenv

// ExecWasmFile is the name of the full skywire command module built for
// GOOS=js (the `skywire` command inside a desk terminal), as installed
// beside the native binary by the package and the binary auto-updater.
const ExecWasmFile = "skywire.wasm"

// ExecWasmPath is where a package install keeps the command module:
// <SkywirePath>/bin/skywire.wasm. `hv serve` and a visor-hosted wasm_serve
// use it when no explicit path is configured and the file exists.
func ExecWasmPath() string {
	if SkywirePath == "" {
		return ""
	}
	return SkywirePath + "/bin/" + ExecWasmFile
}
