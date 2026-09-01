//go:build js && wasm

// Package clidmsg cmd/skywire-cli/commands/dmsg/chat_tui_js.go c5-cli-dmsg
package clidmsg

import (
	"context"
	"errors"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// runChatTUI: the interactive terminal TUI is unavailable in the browser
// build; the command spec (and thus its help) lives untouched in chat.go.
func runChatTUI(_ context.Context, _ *logging.Logger, _ *dmsg.Client,
	_ cipher.PubKey, _ string, _ <-chan incomingChatMsg, _ <-chan error) error {
	return errors.New("dmsg chat: interactive TUI is not available in the browser build")
}
