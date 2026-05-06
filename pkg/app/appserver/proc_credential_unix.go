//go:build !windows
// +build !windows

// Package appserver pkg/app/appserver/proc_credential_unix.go
//
// Sets the cmd's credentials (UID/GID) before exec on POSIX systems.
// Looked up from the system's user database via os/user; the spawned
// process drops to that identity via the kernel's setuid/setgid
// during exec. Requires the visor itself to be allowed to switch
// users — i.e. running as root, or with CAP_SETUID / CAP_SETGID
// granted on the binary.
package appserver

import (
	"fmt"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
)

// applyProcCredentials sets cmd.SysProcAttr.Credential to the
// resolved UID/GID for username/group. Empty username = no-op (the
// spawned process inherits the visor's identity). When username is
// set but group is empty, the user's primary GID is used.
//
// Returns an error if the user/group can't be resolved — the caller
// should treat this as a hard fail since "couldn't drop privileges"
// is exactly the security boundary the operator asked for.
func applyProcCredentials(cmd *exec.Cmd, username, group string) error {
	if username == "" {
		return nil
	}
	u, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("user lookup %q: %w", username, err)
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return fmt.Errorf("parse uid %q: %w", u.Uid, err)
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return fmt.Errorf("parse gid %q: %w", u.Gid, err)
	}
	if group != "" {
		g, err := user.LookupGroup(group)
		if err != nil {
			return fmt.Errorf("group lookup %q: %w", group, err)
		}
		ggid, err := strconv.ParseUint(g.Gid, 10, 32)
		if err != nil {
			return fmt.Errorf("parse gid %q: %w", g.Gid, err)
		}
		gid = ggid
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Credential = &syscall.Credential{
		Uid: uint32(uid),
		Gid: uint32(gid),
		// NoSetGroups=true — don't try to set supplementary groups
		// from the user's group memberships. setgroups(2) requires
		// CAP_SETGID even when not changing the list, and the
		// "additional groups" surface tends to surprise operators
		// more than it helps. Set the primary group; that's enough
		// for filesystem ownership.
		NoSetGroups: true,
	}
	return nil
}
