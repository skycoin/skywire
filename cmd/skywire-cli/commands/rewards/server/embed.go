// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/ui.go
package clirewardsserver

import (
	"embed"
)

//go:embed ui/*
var embeddedFiles embed.FS
