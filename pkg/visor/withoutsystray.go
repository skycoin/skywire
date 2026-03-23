//go:build withoutsystray
// +build withoutsystray

// Package commands pkg/visor/withoutsystray.go
package visor

func runAppSystray() {
	runVisor(nil)
}

func runApp() {
	runVisor(nil)
}

func quit() {

}
