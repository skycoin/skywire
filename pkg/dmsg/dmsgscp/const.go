// Package dmsgscp pkg/dmsg/dmsgscp/const.go: shared constants for the
// scp-over-dmsg utility — wire-protocol framing bytes, default
// dmsg port, and the configurable safety caps the host enforces on
// every transfer.
package dmsgscp

// Dmsg-port constants. We pick port 23 deliberately — adjacent to
// dmsgpty's port 22 — so the two services occupy a contiguous,
// easily-recognized pair on every visor that runs them.
const (
	// DefaultPort is the default dmsg port the dmsgscp Host listens
	// on. Operators may override via config.
	DefaultPort = uint16(23)
)

// Wire-protocol framing bytes. The protocol is a stripped-down
// re-implementation of OpenSSH's scp wire format (the "rcp" subset)
// — a single ack byte after every header and every payload, and an
// error prefix for warnings vs fatal aborts.
const (
	// AckByte is the single-byte acknowledgement the receiver sends
	// after a valid header or completed payload. Zero by design so a
	// raw read of \x00 from the peer means "go ahead".
	AckByte byte = 0x00
	// WarnPrefix marks a non-fatal warning line — sender or receiver
	// may continue after consuming the line.
	WarnPrefix byte = 0x01
	// FatalPrefix marks a fatal error line — the recipient must abort
	// the transfer once it consumes the line.
	FatalPrefix byte = 0x02
)

// Role markers — the first line written on a freshly-dialed stream
// tells the Host whether the caller wants to pull (SOURCE) or push
// (SINK). SSH's classic role flag (`-f` / `-t` on the remote scp
// binary) doesn't map cleanly to a non-shell daemon, so we use an
// explicit line. The format is `<ROLE> <path>\n` with ROLE in
// `SOURCE` or `SINK` and `path` a relative path under the host's
// rootDir.
const (
	// RoleSource is sent by a client requesting a download — the
	// host plays the "source" role (reads file from disk, writes
	// to wire).
	RoleSource = "SOURCE"
	// RoleSink is sent by a client requesting an upload — the host
	// plays the "sink" role (reads payload from wire, writes to disk).
	RoleSink = "SINK"
)

// Safety caps. Apply to the header parser only — there is no
// per-transfer file-size cap. The protocol streams payloads of any
// length the underlying transport accepts. MaxNameLen / MaxHeaderLen
// guard against memory exhaustion from a malicious peer sending a
// multi-megabyte header line; they have no effect on the payload.
const (
	// MaxNameLen caps the basename portion of a `C` or `D` header.
	// Long enough for any reasonable filename, short enough that a
	// malicious peer can't exhaust memory with a multi-megabyte
	// header line.
	MaxNameLen = 255
	// MaxHeaderLen caps the entire header line (`C<mode> <size> <name>\n`).
	// 1 KiB is well above MaxNameLen + the fixed fields.
	MaxHeaderLen = 1024
)
