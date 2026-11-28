// Package releasesig pkg/releasesig/releasesig.go c4-vis-cli
//
// Package releasesig signs and verifies release checksum files with a
// hybrid of Ed25519 and ML-DSA-65 (FIPS 204). A signature is valid only
// when both halves verify, so it holds while either scheme is unbroken.
package releasesig

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/mldsa"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const (
	privateKeyHeader = "skywire release private key v1"
	publicKeyHeader  = "skywire release public key v1"
	signatureHeader  = "skywire release signature v1"
	// mldsaContext domain-separates these signatures (FIPS 204 section 5.2).
	mldsaContext = "skywire-release-v1"
)

// Errors returned by Verify.
var (
	ErrNoTrustedKey = errors.New("releasesig: the signature names no trusted key")
	ErrBadSignature = errors.New("releasesig: signature does not verify")
)

// PublicKey verifies signatures made by the PrivateKey of the same name.
type PublicKey struct {
	Name    string
	ed25519 ed25519.PublicKey
	mldsa   *mldsa.PublicKey
}

// PrivateKey signs release files.
type PrivateKey struct {
	Name    string
	ed25519 ed25519.PrivateKey
	mldsa   *mldsa.PrivateKey
}

// KeyGenerate creates a new key pair under name.
func KeyGenerate(name string) (*PrivateKey, error) {
	if name == "" || strings.ContainsAny(name, " \t\r\n") {
		return nil, fmt.Errorf("releasesig: key name %q must be one word", name)
	}
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	mlKey, err := mldsa.GenerateKey(mldsa.MLDSA65())
	if err != nil {
		return nil, err
	}
	return &PrivateKey{Name: name, ed25519: edKey, mldsa: mlKey}, nil
}

// Public returns the public half of k.
func (k *PrivateKey) Public() *PublicKey {
	return &PublicKey{
		Name:    k.Name,
		ed25519: k.ed25519.Public().(ed25519.PublicKey),
		mldsa:   k.mldsa.PublicKey(),
	}
}

// Sign returns the signature file contents for message.
func (k *PrivateKey) Sign(message []byte) ([]byte, error) {
	mlSig, err := k.mldsa.Sign(rand.Reader, message, &mldsa.Options{Context: mldsaContext})
	if err != nil {
		return nil, err
	}
	return encodeLines(signatureHeader, k.Name,
		ed25519.Sign(k.ed25519, message), mlSig), nil
}

// Verify checks signature over message against trusted and returns the name
// of the key that made it.
func Verify(message, signature []byte, trusted []*PublicKey) (string, error) {
	name, edSig, mlSig, err := decodeLines(signature, signatureHeader)
	if err != nil {
		return "", err
	}
	for _, key := range trusted {
		if key.Name != name {
			continue
		}
		if !ed25519.Verify(key.ed25519, message, edSig) {
			return name, ErrBadSignature
		}
		if err := mldsa.Verify(key.mldsa, message, mlSig, &mldsa.Options{Context: mldsaContext}); err != nil {
			return name, ErrBadSignature
		}
		return name, nil
	}
	return name, fmt.Errorf("%w (%s)", ErrNoTrustedKey, name)
}

// MarshalText encodes k as a private key file. Keep it secret.
func (k *PrivateKey) MarshalText() ([]byte, error) {
	return encodeLines(privateKeyHeader, k.Name, k.ed25519.Seed(), k.mldsa.Bytes()), nil
}

// PrivateKeyParse decodes a private key file.
func PrivateKeyParse(text []byte) (*PrivateKey, error) {
	name, edSeed, mlSeed, err := decodeLines(text, privateKeyHeader)
	if err != nil {
		return nil, err
	}
	if len(edSeed) != ed25519.SeedSize {
		return nil, errors.New("releasesig: bad ed25519 seed length")
	}
	mlKey, err := mldsa.NewPrivateKey(mldsa.MLDSA65(), mlSeed)
	if err != nil {
		return nil, fmt.Errorf("releasesig: mldsa65 key: %w", err)
	}
	return &PrivateKey{Name: name, ed25519: ed25519.NewKeyFromSeed(edSeed), mldsa: mlKey}, nil
}

// MarshalText encodes k as a public key file.
func (k *PublicKey) MarshalText() ([]byte, error) {
	return encodeLines(publicKeyHeader, k.Name, k.ed25519, k.mldsa.Bytes()), nil
}

// PublicKeyParse decodes a public key file.
func PublicKeyParse(text []byte) (*PublicKey, error) {
	name, edKey, mlKey, err := decodeLines(text, publicKeyHeader)
	if err != nil {
		return nil, err
	}
	if len(edKey) != ed25519.PublicKeySize {
		return nil, errors.New("releasesig: bad ed25519 public key length")
	}
	ml, err := mldsa.NewPublicKey(mldsa.MLDSA65(), mlKey)
	if err != nil {
		return nil, fmt.Errorf("releasesig: mldsa65 public key: %w", err)
	}
	return &PublicKey{Name: name, ed25519: ed25519.PublicKey(edKey), mldsa: ml}, nil
}

func encodeLines(header, name string, edPart, mlPart []byte) []byte {
	enc := base64.StdEncoding
	return []byte(fmt.Sprintf("%s\nkey %s\ned25519 %s\nmldsa65 %s\n",
		header, name, enc.EncodeToString(edPart), enc.EncodeToString(mlPart)))
}

func decodeLines(text []byte, header string) (name string, edPart, mlPart []byte, err error) {
	fields := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(text))
	scanner.Buffer(make([]byte, 0, 16384), 16384)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != header {
		return "", nil, nil, fmt.Errorf("releasesig: not a %q file", header)
	}
	for scanner.Scan() {
		label, value, ok := strings.Cut(strings.TrimSpace(scanner.Text()), " ")
		if ok {
			fields[label] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return "", nil, nil, err
	}
	name = fields["key"]
	if name == "" {
		return "", nil, nil, errors.New("releasesig: missing key name")
	}
	if edPart, err = base64.StdEncoding.DecodeString(fields["ed25519"]); err != nil || len(edPart) == 0 {
		return "", nil, nil, errors.New("releasesig: bad ed25519 field")
	}
	if mlPart, err = base64.StdEncoding.DecodeString(fields["mldsa65"]); err != nil || len(mlPart) == 0 {
		return "", nil, nil, errors.New("releasesig: bad mldsa65 field")
	}
	return name, edPart, mlPart, nil
}
