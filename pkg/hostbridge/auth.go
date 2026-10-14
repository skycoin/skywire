// Package hostbridge pkg/hostbridge/auth.go c3-vis-wasm
package hostbridge

import (
	"bufio"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0magnet/yamux"
)

// The first stream of a session pairs it. The tab sends its visor key and
// the bridge a random challenge. An unpaired bridge then gives the first
// visor a pairing secret and records the two; after that the tab proves it
// holds the secret with an HMAC of the challenge, and only that visor is
// served. A shared secret keeps the bridge small: checking a signature by
// the visor key would add the 688 KB of secp256k1 tables.
const helloContext = "skywire-hostbridge-hello-v1:"

const helloTimeout = 15 * time.Second

// Pairing is how a visor identifies itself to the bridge.
type Pairing struct {
	// PK is the visor's public key, in hex.
	PK string
	// Secret is the one the bridge gave this visor when they paired. Empty
	// asks to pair, which only an unpaired bridge accepts. NewClient sets it
	// when the bridge pairs.
	Secret string
}

func helloProof(secret, challenge string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(helloContext + challenge)) //nolint:errcheck
	return hex.EncodeToString(m.Sum(nil))
}

// clientHello runs the tab's side of the first stream.
func clientHello(sess *yamux.Session, p *Pairing) error {
	st, err := sess.Open()
	if err != nil {
		return err
	}
	defer st.Close()                                 //nolint:errcheck
	_ = st.SetDeadline(time.Now().Add(helloTimeout)) //nolint:errcheck
	if err := writeMsg(st, opHello, p.PK, ""); err != nil {
		return err
	}
	br := bufio.NewReader(st)
	challenge, _, err := readReply(br)
	if err != nil {
		return err
	}
	proof := ""
	if p.Secret != "" {
		proof = helloProof(p.Secret, challenge)
	}
	if err := writeMsg(st, opHello, proof, ""); err != nil {
		return err
	}
	secret, _, err := readReply(br)
	if err != nil {
		return err
	}
	if secret != "" {
		p.Secret = secret
	}
	return nil
}

// serverHello runs the bridge's side of the first stream and returns the key
// of the visor the session belongs to.
func (s *Server) serverHello(sess *yamux.Session) (string, error) {
	type accepted struct {
		st  net.Conn
		err error
	}
	ch := make(chan accepted, 1)
	go func() {
		st, err := sess.Accept()
		ch <- accepted{st, err}
	}()
	var a accepted
	select {
	case a = <-ch:
	case <-time.After(helloTimeout):
		return "", errors.New("no hello")
	}
	if a.err != nil {
		return "", a.err
	}
	st := a.st
	defer st.Close()                                 //nolint:errcheck
	_ = st.SetDeadline(time.Now().Add(helloTimeout)) //nolint:errcheck
	br := bufio.NewReader(st)
	op, f, err := readMsg(br)
	if err != nil {
		return "", err
	}
	pk := f[0]
	if op != opHello || !validVisorKey(pk) {
		_ = writeReply(st, errors.New("hostbridge: expected a hello with a visor key"), "", "") //nolint:errcheck
		return "", errors.New("bad hello")
	}
	challenge, err := randomHex(32)
	if err != nil {
		return "", err
	}
	if err := writeReply(st, nil, challenge, ""); err != nil {
		return "", err
	}
	op, f, err = readMsg(br)
	if err != nil {
		return "", err
	}
	if op != opHello {
		return "", errors.New("bad hello")
	}
	secret, err := s.admit(pk, challenge, f[0])
	if err != nil {
		_ = writeReply(st, err, "", "") //nolint:errcheck
		return "", err
	}
	return pk, writeReply(st, nil, secret, "")
}

// admit checks a hello's proof. An unpaired bridge pairs with pk and returns
// the new secret for it.
func (s *Server) admit(pk, challenge, proof string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paired.PK == "" {
		secret, err := randomHex(32)
		if err != nil {
			return "", err
		}
		p := Pairing{PK: pk, Secret: secret}
		if err := savePairing(s.cfg.PairFile, p); err != nil {
			return "", err
		}
		s.paired = p
		s.logf("paired with visor %s", pk)
		return secret, nil
	}
	if pk != s.paired.PK {
		return "", fmt.Errorf("hostbridge: paired with visor %s; run the bridge with --unpair to pair another", s.paired.PK)
	}
	if hmac.Equal([]byte(proof), []byte(helloProof(s.paired.Secret, challenge))) {
		return "", nil
	}
	return "", errors.New("hostbridge: wrong pairing secret; run the bridge with --unpair to pair this visor again")
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// validVisorKey reports whether pk looks like a visor public key: 33 bytes,
// compressed, in hex.
func validVisorKey(pk string) bool {
	b, err := hex.DecodeString(pk)
	return err == nil && len(b) == 33 && (b[0] == 2 || b[0] == 3)
}

// savePairing records p in path, readable only by the user. An empty path
// keeps the pairing in memory.
func savePairing(path string, p Pairing) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(p.PK+" "+p.Secret+"\n"), 0o600)
}

// loadPairing reads the pairing savePairing wrote, if any.
func loadPairing(path string) (Pairing, error) {
	if path == "" {
		return Pairing{}, nil
	}
	b, err := os.ReadFile(path) //nolint:gosec // the operator names the pairing file
	if errors.Is(err, os.ErrNotExist) {
		return Pairing{}, nil
	}
	if err != nil {
		return Pairing{}, err
	}
	pk, secret, ok := strings.Cut(strings.TrimSpace(string(b)), " ")
	if !ok || !validVisorKey(pk) || secret == "" {
		return Pairing{}, fmt.Errorf("hostbridge: pairing file %s is damaged; run the bridge with --unpair", path)
	}
	return Pairing{PK: pk, Secret: secret}, nil
}
