// Package visor pkg/visor/init_dmsg_relay_local.go c3-vis-core
//
// The LOCAL dmsg relay acceptor: the third pipe into the same relay
// init_dmsg_relay.go already serves over skynet, and the only one that does not
// involve the network at all.
//
// It exists for the standalone dmsg client that has to keep its key. The
// dmsgweb proxy is the case that forced it: it runs beside the visor under a
// key the survey whitelist knows, so rotating the key is not an option, and yet
// every one of those processes holds its own sessions to dmsg servers and
// publishes its own discovery entry — a whole second client's worth of server
// capacity, per process, on a host that already has a visor with sessions and
// transports. Attached here it keeps the key, holds exactly one session (to
// this visor, over a unix socket), publishes nothing, and its streams ride this
// visor's sessions.
//
// AUTHORIZATION — read dmsg.DmsgLocalRelayConfig's comment for the full model.
// In short: the unix socket's 0600 mode means the attacher is already running
// as the visor's user, which is a process that could read the visor's secret
// key off disk anyway; and the optional allowed_keys list is real
// authentication, because the dmsg session handshake is Noise XK and proves the
// attacher holds the secret key for the PK it claims. The loopback listener has
// no filesystem gate, so it is refused unless allowed_keys is non-empty.
package visor

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgc/spec"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/util/osutil"
)

const (
	// defaultLocalRelaySocketName is the unix socket the local relay acceptor
	// binds under the visor's local_path when dmsg.local_relay.socket is
	// unset.
	defaultLocalRelaySocketName = "dmsg_relay.sock"

	// defaultLocalRelaySocketMode is the socket's file mode when
	// dmsg.local_relay.socket_mode is unset: owner only. This is the gate —
	// see the package comment — so it is deliberately not 0666 and not 0660.
	defaultLocalRelaySocketMode os.FileMode = 0600

	// localRelaySocketDirMode is the mode the socket's PARENT directory is
	// created with. net.Listen("unix") creates the socket file with
	// 0777&^umask and there is no way to hand it a mode up front, so between
	// the bind and the chmod below the socket is briefly whatever the umask
	// allows. A 0700 directory closes that window: an unreachable path cannot
	// be connected to whatever the file's own bits say for those few
	// microseconds.
	//
	// It closes it only for a directory this visor CREATES, though —
	// os.MkdirAll leaves an existing one alone, and the default path sits
	// under local_path, which usually already exists. See
	// warnIfLocalRelayDirPermissive for what is done about that and why it is
	// a warning rather than a refusal.
	localRelaySocketDirMode os.FileMode = 0700

	// maxUnixSocketPath is the shortest sockaddr_un.sun_path across the
	// platforms this runs on (104 on the BSDs/macOS, 108 on Linux, both
	// including the NUL). Used only to turn a bare EINVAL into a message that
	// names the actual problem.
	maxUnixSocketPath = 103
)

// initLocalDmsgRelay binds the configured local relay acceptor(s) and serves
// each as a dmsg relay on dmsgC. No config (the default) means no listener.
//
// Errors bind-time rather than at first attach: a misconfigured socket path or
// an unauthenticated loopback listener must be loud at boot, not a silent
// missing feature discovered when the attaching process cannot connect. They
// are logged and skipped rather than failing visor startup — a relay acceptor
// nobody has attached to yet is not worth refusing to boot over.
func (v *Visor) initLocalDmsgRelay(ctx context.Context, dmsgC *dmsg.Client) {
	if v.conf == nil || v.conf.Dmsg == nil {
		return
	}
	conf := v.conf.Dmsg.LocalRelay
	if conf == nil || !conf.Enabled {
		return
	}
	log := v.MasterLogger().PackageLogger("dmsg_relay_local")

	allow, err := localRelayAllow(conf, v.conf.PK)
	if err != nil {
		log.WithError(err).Error("dmsg local relay disabled")
		return
	}

	if sock := localRelaySocketPath(conf, v.conf.LocalPath); sock != "" {
		if lis, err := listenLocalRelaySocket(sock, conf.SocketMode, log); err != nil {
			log.WithError(err).WithField("socket", sock).Error("dmsg local relay: unix listener failed")
		} else {
			log.WithField("socket", sock).WithField("allowed_keys", len(conf.AllowedKeys)).
				Info("dmsg local relay listening")
			v.serveLocalRelay(ctx, dmsgC, lis, allow, log)
		}
	}

	if conf.TCPAddress != "" {
		if err := checkLocalRelayTCP(conf); err != nil {
			log.WithError(err).WithField("tcp_address", conf.TCPAddress).
				Error("dmsg local relay: loopback listener refused")
		} else if lis, err := net.Listen("tcp", conf.TCPAddress); err != nil {
			log.WithError(err).WithField("tcp_address", conf.TCPAddress).
				Error("dmsg local relay: loopback listener failed")
		} else {
			log.WithField("tcp_address", conf.TCPAddress).WithField("allowed_keys", len(conf.AllowedKeys)).
				Info("dmsg local relay listening")
			v.serveLocalRelay(ctx, dmsgC, lis, allow, log)
		}
	}
}

// serveLocalRelay runs one acceptor and registers its shutdown. The listener is
// closed on the close stack as well as on ctx: a unix socket left bound holds
// its path, and the NEXT visor start would have to unlink a live-looking
// socket to get it back.
func (v *Visor) serveLocalRelay(ctx context.Context, dmsgC *dmsg.Client, lis net.Listener, allow func(cipher.PubKey) bool, log *logging.Logger) {
	go func() {
		if err := dmsgC.ServeLocalRelay(ctx, lis, skyenv.DmsgRelayPort, allow); err != nil {
			log.WithError(err).Warn("dmsg local relay acceptor stopped")
		}
	}()
	v.pushCloseStack("dmsg_relay_local", func() error {
		return lis.Close()
	})
}

// localRelaySocketPath resolves the unix socket path: the configured one, the
// default under the visor's local_path, or "" when the operator set "-" to run
// with the loopback listener only.
func localRelaySocketPath(conf *spec.DmsgLocalRelayConfig, localPath string) string {
	switch conf.Socket {
	case "-":
		return ""
	case "":
		if localPath == "" {
			localPath = skyenv.LocalPath
		}
		return filepath.Join(localPath, defaultLocalRelaySocketName)
	default:
		return conf.Socket
	}
}

// listenLocalRelaySocket binds the unix socket with the configured mode.
//
// The stale-socket unlink is the same one the dmsgpty CLI listener does: a
// visor killed without running its close stack leaves the path behind, and
// bind() on an existing path is EADDRINUSE whether or not anything is
// listening. Unlinking is safe here because the path is the visor's own and a
// second visor sharing one local_path is already broken for other reasons.
func listenLocalRelaySocket(path string, mode string, log *logging.Logger) (net.Listener, error) {
	fileMode, err := parseSocketMode(mode)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, localRelaySocketDirMode); err != nil {
		return nil, fmt.Errorf("failed to prepare directory for dmsg local relay socket: %w", err)
	}
	// MkdirAll applies its mode only to directories it CREATES. A pre-existing
	// parent — the common case, since the default path sits under the visor's
	// local_path — keeps whatever mode it already had, so the window-closing
	// property described on localRelaySocketDirMode is not guaranteed and has
	// to be checked rather than assumed. Observed in practice: ~/.skywire
	// already existed as 0705, world-traversable.
	//
	// This is a narrow concern, not a hole: the socket's FINAL mode is 0600
	// either way, and connecting to a unix socket needs write permission, so
	// under the usual umask 022 the file is 0755 for the few microseconds
	// before the chmod and nobody else can connect to it even then. It only
	// bites under a permissive umask (002/000), where those bytes are 0775 or
	// 0777. Warn rather than fail: the operator may have deliberately relaxed
	// the directory, and refusing to serve would be a worse answer than
	// naming the exposure.
	warnIfLocalRelayDirPermissive(dir, log)
	if err := osutil.UnlinkSocketFiles(path); err != nil {
		return nil, fmt.Errorf("failed to unlink stale dmsg local relay socket: %w", err)
	}
	lis, err := net.Listen("unix", path)
	if err != nil {
		// A unix socket path is bounded by sockaddr_un.sun_path (107 usable
		// bytes on Linux, fewer on the BSDs), and exceeding it fails with a
		// bare EINVAL — "invalid argument" — which reads like a bug in the
		// config parser rather than a path that is simply too long. Say so.
		if len(path) >= maxUnixSocketPath {
			return nil, fmt.Errorf("failed to bind dmsg local relay socket (path is %d bytes; unix sockets allow about %d): %w", len(path), maxUnixSocketPath, err)
		}
		return nil, err
	}
	// Chmod AFTER bind — the socket file does not exist before it. See
	// localRelaySocketDirMode for why the window this opens is closed by the
	// parent directory rather than by ordering.
	//
	// A failure here is fatal to the listener, NOT a warning: the whole
	// authorization story for a socket with no allowed_keys is its mode, and
	// serving one whose mode is unknown would be exactly the
	// unauthenticated local path this feature must not create.
	if err := os.Chmod(path, fileMode); err != nil {
		_ = lis.Close() //nolint:errcheck
		return nil, fmt.Errorf("failed to set dmsg local relay socket mode: %w", err)
	}
	return lis, nil
}

// parseSocketMode parses an octal file-mode string ("0600"). Empty is the
// default. It is a string in the config because JSON has no octal literal and
// 0600 written as a JSON number is six hundred.
func parseSocketMode(mode string) (os.FileMode, error) {
	if mode == "" {
		return defaultLocalRelaySocketMode, nil
	}
	m, err := strconv.ParseUint(mode, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("dmsg.local_relay.socket_mode %q is not an octal file mode: %w", mode, err)
	}
	return os.FileMode(m), nil
}

// checkLocalRelayTCP refuses a loopback listener that would be an
// unauthenticated path to this visor's transports.
//
// Two conditions, both load-bearing. A non-loopback bind address would expose
// the acceptor to the network, where the only gate left is the key list — and a
// key list is an allowlist, not a rate limit or a DoS defense. And with NO key
// list there is no gate at all: unlike the unix socket, a TCP port carries no
// evidence about who opened it, so every local user (and, on a shared network
// namespace, every co-tenant container) could attach under any key it liked.
func checkLocalRelayTCP(conf *spec.DmsgLocalRelayConfig) error {
	if len(conf.AllowedKeys) == 0 {
		return fmt.Errorf("dmsg.local_relay.tcp_address requires a non-empty allowed_keys: a TCP listener has no filesystem gate")
	}
	host, _, err := net.SplitHostPort(conf.TCPAddress)
	if err != nil {
		return fmt.Errorf("dmsg.local_relay.tcp_address %q is not a host:port: %w", conf.TCPAddress, err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("dmsg.local_relay.tcp_address %q must name a loopback IP (or localhost)", conf.TCPAddress)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("dmsg.local_relay.tcp_address %q is not a loopback address", conf.TCPAddress)
	}
	return nil
}

// localRelayAllow builds the admission callback for the local acceptor — the
// same func(cipher.PubKey) bool the skynet acceptor hands AcceptRelaySession,
// consulted at the same point: after the Noise handshake has proven the peer's
// key.
//
// It is deliberately NOT relayPeerAllowed. That callback answers a different
// question — "is this REMOTE peer one we have a relationship with" — by
// consulting the peer whitelist, the visors we manage, and the transports we
// hold, none of which a local process has or needs. Here the listener is the
// relationship; allowed_keys narrows it when the listener alone is not enough.
//
// The visor's own key is refused outright. Two dmsg clients sharing one key
// evict each other from every server they share, which is a failure mode this
// deployment has paid for before; the local relay must not be a new way to
// arrange it.
func localRelayAllow(conf *spec.DmsgLocalRelayConfig, selfPK cipher.PubKey) (func(cipher.PubKey) bool, error) {
	allowed := make(map[cipher.PubKey]struct{}, len(conf.AllowedKeys))
	for _, pk := range conf.AllowedKeys {
		if pk.Null() {
			return nil, fmt.Errorf("dmsg.local_relay.allowed_keys contains a null public key")
		}
		if pk == selfPK {
			return nil, fmt.Errorf("dmsg.local_relay.allowed_keys contains this visor's own key %s", pk)
		}
		allowed[pk] = struct{}{}
	}
	return func(pk cipher.PubKey) bool {
		if pk.Null() || pk == selfPK {
			return false
		}
		if len(allowed) == 0 {
			return true // the listener is the gate; see the package comment
		}
		_, ok := allowed[pk]
		return ok
	}, nil
}

// warnIfLocalRelayDirPermissive logs when the socket's parent directory is
// reachable by group or other, which weakens the bind→chmod window described in
// listenLocalRelaySocket. Best-effort: a stat failure is not worth failing the
// listener over, since the socket's own 0600 remains the operative gate.
func warnIfLocalRelayDirPermissive(dir string, log *logging.Logger) {
	fi, err := os.Stat(dir)
	if err != nil {
		return
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		log.WithField("dir", dir).
			WithField("mode", fmt.Sprintf("%#o", perm)).
			Warnf("dmsg local-relay socket directory is reachable by group/other; "+
				"the socket itself is %#o so this is only exposed under a permissive umask, "+
				"but chmod 0700 %s closes it entirely", defaultLocalRelaySocketMode, dir)
	}
}
