// Package commands cmd/skywire/commands/autoconfig_skyenv.go c4-vis-cli
//
// Thin aliases over pkg/skywireconfig/skyenvfile, which owns editing the
// bash-style skyenv file (/etc/skywire.conf). The visor shares that
// package so a paired hypervisor survives the next autoconfig run.
package commands

import "github.com/skycoin/skywire/pkg/skywireconfig/skyenvfile"

type skyenvEdit = skyenvfile.Edit

func updateSkyenvFile(path string, edits []skyenvEdit) error { return skyenvfile.Update(path, edits) }

func formatSkyenvBool(b bool) string { return skyenvfile.FormatBool(b) }

func formatSkyenvString(s string) string { return skyenvfile.FormatString(s) }

func formatSkyenvBashArray(csv string) string { return skyenvfile.FormatBashArray(csv) }

func formatSkyenvInt(i int) string { return skyenvfile.FormatInt(i) }

func defaultSkyenvPath() string { return skyenvfile.DefaultPath() }
