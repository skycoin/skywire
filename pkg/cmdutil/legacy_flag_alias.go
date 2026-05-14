// Package cmdutil pkg/cmdutil/legacy_flag_alias.go: pflag NormalizeFunc
// shared by every svc command that has been through a flag-rename. Maps
// historical flag spellings to their current canonical kebab-case form
// so any compose.yaml / systemd unit / shell wrapper that still passes
// the old name keeps working invisibly.
//
// The renamed flag is registered ONCE under its current name; this
// normalizer rewrites the legacy form at parse time, so the legacy form
// doesn't appear in --help (no surface area pollution) but still
// resolves to the same target.
package cmdutil

import "github.com/spf13/pflag"

// legacyFlagAliases maps deprecated flag names → current canonical name.
// Add an entry here whenever a flag gets renamed in a backward-incompatible
// way; the corresponding cobra command must SetNormalizeFunc to
// LegacySvcFlagNormalizer to pick up the alias.
//
// Keep entries lowercase on the right side; pflag's NormalizedName is
// case-sensitive but its lookup uses the normalized form verbatim.
var legacyFlagAliases = map[string]string{
	// 2026-05-14: --dmsgPort camelCase outlier across every svc command.
	// Renamed to --dmsg-port for consistency with --dmsg-disc,
	// --dmsg-server-type, etc. Old form kept as a silent alias.
	"dmsgPort": "dmsg-port",
}

// LegacySvcFlagNormalizer is the NormalizeFunc every svc command's
// FlagSet should install via Flags().SetNormalizeFunc. Maps legacy flag
// names from the table above to their current canonical form; falls
// through unchanged for everything else.
func LegacySvcFlagNormalizer(_ *pflag.FlagSet, name string) pflag.NormalizedName {
	if canonical, ok := legacyFlagAliases[name]; ok {
		return pflag.NormalizedName(canonical)
	}
	return pflag.NormalizedName(name)
}
