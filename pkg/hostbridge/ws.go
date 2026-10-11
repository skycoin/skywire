// Package hostbridge pkg/hostbridge/ws.go c3-vis-wasm
package hostbridge

import (
	"bufio"
	"crypto/sha1" //nolint:gosec // RFC 6455 section 4.2.2 fixes SHA-1 for the accept key
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// wsGUID is from RFC 6455 section 1.3.
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA

	maxHeaderBytes = 8 << 10
	maxFrame       = 1 << 20
)

var errForbidden = errors.New("forbidden")

// upgrade reads the HTTP request on c and completes the WebSocket handshake
// when the host, origin and token check out. Anything else gets a 403.
func (s *Server) upgrade(c net.Conn) (*wsConn, error) {
	_ = c.SetDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
	br := bufio.NewReaderSize(c, 4096)
	line, err := readLine(br)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(line, " ")
	if len(parts) != 3 || parts[0] != "GET" {
		return nil, reject(c, errForbidden)
	}
	hdr := map[string]string{}
	total := len(line)
	for {
		l, err := readLine(br)
		if err != nil {
			return nil, err
		}
		if l == "" {
			break
		}
		total += len(l)
		if total > maxHeaderBytes {
			return nil, reject(c, errForbidden)
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		hdr[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	if !s.allowedHost(hdr["host"]) || !s.allowedOrigin(hdr["origin"]) || !s.validToken(parts[1]) {
		return nil, reject(c, errForbidden)
	}
	key := hdr["sec-websocket-key"]
	if key == "" || !strings.EqualFold(hdr["upgrade"], "websocket") || hdr["sec-websocket-version"] != "13" {
		return nil, reject(c, errForbidden)
	}
	h := sha1.Sum([]byte(key + wsGUID)) //nolint:gosec
	resp := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(h[:]) + "\r\n\r\n"
	if _, err := io.WriteString(c, resp); err != nil {
		return nil, err
	}
	_ = c.SetDeadline(time.Time{}) //nolint:errcheck
	return &wsConn{Conn: c, br: br}, nil
}

func readLine(br *bufio.Reader) (string, error) {
	l, err := br.ReadString('\n')
	if err != nil {
		return "", err
	}
	if len(l) > maxHeaderBytes {
		return "", errForbidden
	}
	return strings.TrimRight(l, "\r\n"), nil
}

func reject(c net.Conn, err error) error {
	_, _ = io.WriteString(c, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n") //nolint:errcheck
	return err
}

// allowedHost refuses any Host but a loopback one, which stops DNS rebinding.
func (s *Server) allowedHost(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	h = strings.Trim(h, "[]")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// allowedOrigin passes a request without Origin, which no browser sends, and
// any listed origin. Browsers always send Origin on a WebSocket handshake.
func (s *Server) allowedOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	for _, o := range s.cfg.Origins {
		if o == "*" || strings.EqualFold(o, origin) {
			return true
		}
	}
	return false
}

func (s *Server) validToken(target string) bool {
	tok := strings.TrimPrefix(target, "/")
	if i := strings.IndexByte(tok, '?'); i >= 0 {
		tok = tok[:i]
	}
	return subtle.ConstantTimeCompare([]byte(tok), []byte(s.cfg.Token)) == 1
}

// wsConn is the server end of a WebSocket as a byte stream. Each binary
// message is appended to the stream, so message boundaries do not matter.
type wsConn struct {
	net.Conn
	br  *bufio.Reader
	rmu sync.Mutex
	wmu sync.Mutex
	buf []byte
	eof bool
}

func (w *wsConn) Read(p []byte) (int, error) {
	w.rmu.Lock()
	defer w.rmu.Unlock()
	for len(w.buf) == 0 {
		if w.eof {
			return 0, io.EOF
		}
		if err := w.readFrame(); err != nil {
			return 0, err
		}
	}
	n := copy(p, w.buf)
	w.buf = w.buf[n:]
	return n, nil
}

func (w *wsConn) readFrame() error {
	var h [2]byte
	if _, err := io.ReadFull(w.br, h[:]); err != nil {
		return err
	}
	op := h[0] & 0x0F
	if h[1]&0x80 == 0 {
		return errors.New("hostbridge: unmasked client frame")
	}
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(w.br, b[:]); err != nil {
			return err
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(w.br, b[:]); err != nil {
			return err
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > maxFrame {
		return errors.New("hostbridge: frame too large")
	}
	var mask [4]byte
	if _, err := io.ReadFull(w.br, mask[:]); err != nil {
		return err
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(w.br, payload); err != nil {
		return err
	}
	for i := range payload {
		payload[i] ^= mask[i&3]
	}
	switch op {
	case opBinary, opText, opContinuation:
		w.buf = payload
	case opPing:
		return w.writeFrame(opPong, payload)
	case opClose:
		w.eof = true
		_ = w.writeFrame(opClose, nil) //nolint:errcheck
	case opPong:
	default:
		return errors.New("hostbridge: unknown opcode")
	}
	return nil
}

func (w *wsConn) Write(p []byte) (int, error) {
	for off := 0; off < len(p); off += maxFrame {
		end := min(off+maxFrame, len(p))
		if err := w.writeFrame(opBinary, p[off:end]); err != nil {
			return off, err
		}
	}
	return len(p), nil
}

func (w *wsConn) writeFrame(op byte, p []byte) error {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	hdr := make([]byte, 2, 10+len(p))
	hdr[0] = 0x80 | op
	switch n := len(p); {
	case n < 126:
		hdr[1] = byte(n)
	case n <= 0xFFFF:
		hdr[1] = 126
		hdr = binary.BigEndian.AppendUint16(hdr, uint16(n))
	default:
		hdr[1] = 127
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	_, err := w.Conn.Write(append(hdr, p...))
	return err
}
