// Package main example/test/server.go
package main

import (
	"fmt"

	skycipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/encrypt"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

func main() {
	pk1, sk1 := cipher.GenerateKeyPair()
	fmt.Println("PK1:", pk1.String())
	fmt.Println("SK1:", sk1.String())
	// Output :
	// PK1: 0356fa22dcc23a923eb850257ec2a8686187899cb962eace05e7b01a41f83a06d5
	// SK1: 798dcd28eb524910ad0ea7fc6cd541b3e0145e19fd5c0e1d9e40870fd11517c5

	pk2, sk2 := cipher.GenerateKeyPair()
	fmt.Println("PK2:", pk2.String())
	fmt.Println("SK2:", sk2.String())
	// Output :
	// PK2: 029f6f747b2ee297e71d0a8db723bbd76c439912a72f89966b002b05d4d0ab315f
	// SK2: 67bcf16ca23bf6492c867017409250785b0f3abaeb0756ac14bd922757f31398

	log := logging.MustGetLogger("test")
	sharedSec1, err := skycipher.ECDH(skycipher.PubKey(pk1), skycipher.SecKey(sk2))
	if err != nil {
		log.WithError(err).Error("failed to created ECDH")
	}
	fmt.Println("sharedSec1:", string(sharedSec1))
	// Output :
	// sharedSec1: ]w'&jO'''9'''ֆ[q'p'' p '''n'

	sharedSec2, err := skycipher.ECDH(skycipher.PubKey(pk2), skycipher.SecKey(sk1))
	if err != nil {
		log.WithError(err).Error("failed to created ECDH")
	}
	fmt.Println("sharedSec2:", string(sharedSec2))
	// Output :
	// sharedSec2: ]w'&jO'''9'''ֆ[q'p'' p '''n'

	message := "Here is a string...."

	cryptor := encrypt.DefaultScryptChacha20poly1305
	enc, err := cryptor.Encrypt([]byte(message), sharedSec1)
	if err != nil {
		log.WithError(err).Error("failed to encrypt")
	}
	fmt.Println("enc:", string(enc))
	// Output :
	// enc: dgB7Im4iOjEwNDg1NzYsInIiOjgsInAiOjEsImtleUxlbiI6MzIsInNhbHQiOiJ1QVVKWGFPK2pjWkZoM1hGT3RMOHNjQ2s4NnNMTnBnUXJiMEVPN3RUaVVVPSIsIm5vbmNlIjoiMWhwb3Z5RWRXMUE3MHFxMyJ9NRRIFwmfNWb8Zhz7ZSp1aehp37/szkMps1wzIEg0Xr9/rQvo

	dec, err := cryptor.Decrypt(enc, sharedSec2)
	if err != nil {
		log.WithError(err).Error("failed to decrypt")
	}
	fmt.Println("dec:", string(dec))
	// Output :
	// dec: Here is a string....
}
