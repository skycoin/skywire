// Package manager pkg/manager/api.go
package manager

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"sync"

	"github.com/skycoin/skycoin/src/cipher/encrypt"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/setup"
)

// // API represents visor API.
//
//	type API interface {
//		AddTransport(remote cipher.PubKey, tpType string, timeout time.Duration) (*setup.TransportSummary, error)
//		RemoveTransport(tid uuid.UUID) error
//		GetTransports() ([]*setup.TransportSummary, error)
//	}

// generateRandomBytes returns securely generated random bytes.
// It will return an error if the system's secure random
// number generator fails to function correctly, in which
// case the caller should not continue.
func generateRandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	// Note that err == nil only if we read len(b) bytes.
	if err != nil {
		return nil, err
	}

	return b, nil
}

// generateRandomString returns a URL-safe, base64 encoded
// securely generated random string.
func generateRandomString(s int) string {
	b, _ := generateRandomBytes(s) //nolint
	return base64.URLEncoding.EncodeToString(b)
}

// ManagementInterface contains the API that is served over RPC for authorized managers
type ManagementInterface struct {
	log          *logging.Logger
	tpSetup      *setup.API
	sharedSec    []byte
	remotePK     cipher.PubKey
	localSK      cipher.SecKey
	cryptor      encrypt.ScryptChacha20poly1305
	challengeMsg string
	readyCh      chan struct{} // push here when challenge is completed - protected by 'readyOnce'
	readyOnce    sync.Once     // ensures we only push to 'readyCh' once
}

// NewManagementInterface returns ManagementInterface
func NewManagementInterface(log *logging.Logger, tpSetup *setup.API, remotePK cipher.PubKey, localSK cipher.SecKey, sharedSec []byte) *ManagementInterface {

	m := &ManagementInterface{
		log:          log,
		tpSetup:      tpSetup,
		sharedSec:    sharedSec,
		remotePK:     remotePK,
		localSK:      localSK,
		cryptor:      encrypt.DefaultScryptChacha20poly1305,
		challengeMsg: generateRandomString(20),
		readyCh:      make(chan struct{}, 1),
	}

	return m
}

// Connection is used to send and receive the RPC challenge and response
type Connection struct {
	Challenge string `json:"challenge,omitempty"`
	Response  string `json:"response,omitempty"`
}

// Challenge sends the requesting visor an encrypted challenge string
func (mi *ManagementInterface) Challenge() ([]byte, error) {
	mi.log.Debugf("Challenge request received from %v", mi.remotePK)
	sendC := Connection{
		Challenge: mi.challengeMsg,
	}
	byteArray, err := json.Marshal(sendC)
	if err != nil {
		return nil, err
	}
	return mi.cryptor.Encrypt(byteArray, mi.sharedSec)
}

// Response receives the response of the challenge and verifies it
func (mi *ManagementInterface) Response(resp []byte) (bool, error) {
	mi.log.Debugf("Challenge Response received from %v", mi.remotePK)
	byteArray, err := mi.cryptor.Decrypt(resp, mi.sharedSec)
	if err != nil {
		return false, err
	}
	var con Connection
	err = json.Unmarshal(byteArray, &con)
	if err != nil {
		return false, err
	}
	if con.Response != mi.challengeMsg {
		return false, nil
	}
	mi.readyOnce.Do(func() { close(mi.readyCh) })
	mi.log.Debugf("Challenge solved by %v", mi.remotePK)
	return true, nil
}
