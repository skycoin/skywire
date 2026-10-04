//go:build js && wasm

// Package deskhost pkg/wasmhv/deskhost/desk_mail_js.go c4-wasm-desk
//
// The ☰ mail entry. Mail is a visor app, so its window is the dashboard's
// Mail tab in the browser, the same page a native visor serves.
package deskhost

import "github.com/0magnet/desk"

func registerMailApp() {
	desk.Register(desk.App{
		Name: "mail", Title: "mail",
		Help: "e-mail over skywire: this visor's mailbox, in the dashboard",
		Run: func([]string) error {
			_, err := desk.Launch("browser", mailURL)
			return err
		},
	})
}
