// Package cliutil cmd/skywire-cli/cliutil/transport.go c4-vis-cli
// Shared transport vocabulary for the remote-access commands
// (pty exec/start, pty shell, dmsg scp, pty fs mount). Each command
// supports a ragged subset of these transports — see each command's
// help for its own matrix. This file only defines the vocabulary and
// the flag-vs-target-scheme precedence; it does not itself dial.
package cliutil

import (
	"fmt"
	"strings"
)

// Canonical transport names shared across the remote-access commands.
//
//	auto   each command's current default behavior.
//	dmsg   via-visor dmsg.
//	skynet via-visor skynet.
//	tcp    direct noise-TCP to a host:port (needs a host:port target).
const (
	TransportAuto   = "auto"
	TransportDmsg   = "dmsg"
	TransportSkynet = "skynet"
	TransportTCP    = "tcp"
)

// targetSchemes are the recognized `<scheme>://` prefixes on a
// positional remote-access target. They mirror the --transport
// vocabulary minus auto (auto is a flag-only default, never spelled on
// a target).
var targetSchemes = []string{TransportDmsg, TransportSkynet, TransportTCP}

// SplitTargetScheme detects a leading dmsg:// / skynet:// / tcp://
// scheme on a positional target. On a recognized prefix it returns the
// scheme name (without "://") and the remainder after "://"; otherwise
// ("", target) with target unchanged. Bare targets — a plain PK,
// <pk>:path, <pk>@host:port — therefore pass through byte-identical, so
// every pre-existing target form keeps working.
func SplitTargetScheme(target string) (scheme, rest string) {
	for _, s := range targetSchemes {
		pfx := s + "://"
		if strings.HasPrefix(target, pfx) {
			return s, target[len(pfx):]
		}
	}
	return "", target
}

// ResolveTransport reconciles an explicit --transport / --scheme flag
// with a scheme carried on the positional target, returning the
// effective transport.
//
//	flagVal      the --transport value; "" is treated as auto.
//	flagChanged  whether the user explicitly set the flag.
//	targetScheme the scheme parsed from the target ("" if the target
//	             is bare).
//
// Precedence:
//   - target bare              -> flagVal (default auto).
//   - only the target scheme'd -> the target scheme.
//   - both, and they agree     -> that transport.
//   - both, and they conflict  -> error (an explicit non-auto flag that
//     disagrees with the target scheme is ambiguous — the caller must
//     drop one).
//
// An explicit --transport auto never conflicts: an operator asking for
// auto alongside a scheme'd target is read as "let the target decide".
func ResolveTransport(flagVal string, flagChanged bool, targetScheme string) (string, error) {
	if flagVal == "" {
		flagVal = TransportAuto
	}
	if targetScheme == "" {
		return flagVal, nil
	}
	if flagChanged && flagVal != TransportAuto && flagVal != targetScheme {
		return "", fmt.Errorf("target scheme %s:// conflicts with --transport %s; specify one", targetScheme, flagVal)
	}
	return targetScheme, nil
}
