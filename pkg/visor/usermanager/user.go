// Package usermanager user.go
package usermanager

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
	"unicode"

	"go.etcd.io/bbolt"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

const (
	boltTimeout        = 10 * time.Second
	boltUserBucketName = "users"
	passwordSaltLen    = 16
	minPasswordLen     = 6
	maxPasswordLen     = 64
	ownerRW            = 0600
	ownerRWX           = 0700
)

// Errors returned by UserStore.
var (
	ErrBadPasswordLen = fmt.Errorf("password length should be between %d and %d chars", minPasswordLen, maxPasswordLen)
	ErrSimplePassword = fmt.Errorf("password must have at least one upper, lower, digit and special character")
	ErrUserExists     = fmt.Errorf("username already exists")
	ErrNameNotAllowed = fmt.Errorf("name not allowed")
	ErrNonASCII       = fmt.Errorf("non-ASCII character found")
)

// nolint: gochecknoinits
func init() {
	gob.Register(User{})
}

// User represents a user of the hypervisor.
type User struct {
	Name   string
	PwSalt []byte
	PwHash cipher.SHA256
}

// SetName checks the provided name, and sets the name if format is valid.
func (u *User) SetName(name string) bool {
	if !checkUsernameFormat(name) {
		return false
	}

	u.Name = name

	return true
}

// SetPassword checks the provided password, and sets the password if format is valid.
func (u *User) SetPassword(password string) error {
	if err := checkPasswordFormat(password); err != nil {
		return err
	}

	u.PwSalt = cipher.RandByte(passwordSaltLen)
	u.PwHash = cipher.SumSHA256(append([]byte(password), u.PwSalt...))

	return nil
}

// VerifyPassword verifies the password input with hash and salt.
func (u *User) VerifyPassword(password string) bool {
	return cipher.SumSHA256(append([]byte(password), u.PwSalt...)) == u.PwHash
}

// Encode encodes the user to bytes.
func (u *User) Encode() ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(u); err != nil {
		return nil, fmt.Errorf("unexpected user encode error: %w", err)
	}

	return buf.Bytes(), nil
}

// DecodeUser decodes the user from bytes.
func DecodeUser(raw []byte) (*User, error) {
	var user User
	if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(&user); err != nil {
		return nil, fmt.Errorf("unexpected decode user error: %w", err)
	}

	return &user, nil
}

// UserStore stores users.
type UserStore interface {
	User(name string) (*User, error)
	AddUser(user User) error
	SetUser(user User) error
	RemoveUser(name string) error
	Close() error
}

// BoltUserStore implements UserStore, storing users in a bbolt database file.
type BoltUserStore struct {
	*bbolt.DB
}

// NewBoltUserStore creates a new BoltUserStore.
func NewBoltUserStore(path string) (*BoltUserStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), os.FileMode(ownerRWX)); err != nil {
		return nil, err
	}

	db, err := bbolt.Open(path, os.FileMode(ownerRW), &bbolt.Options{Timeout: boltTimeout})
	if err != nil {
		return nil, err
	}

	err = db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(boltUserBucketName))
		return err
	})

	return &BoltUserStore{DB: db}, err
}

// User obtains a single user. Returns nil if user does not exist.
func (s *BoltUserStore) User(name string) (user *User, err error) {
	err = s.View(func(tx *bbolt.Tx) error {
		users := tx.Bucket([]byte(boltUserBucketName))
		rawUser := users.Get([]byte(name))
		if rawUser == nil {
			return nil
		}

		user, err = DecodeUser(rawUser)
		return err
	})

	return user, err
}

// AddUser adds a new user.
func (s *BoltUserStore) AddUser(user User) error {
	return s.Update(func(tx *bbolt.Tx) error {
		users := tx.Bucket([]byte(boltUserBucketName))
		if users.Get([]byte(user.Name)) != nil {
			return ErrUserExists
		}

		encoded, err := user.Encode()
		if err != nil {
			return err
		}

		return users.Put([]byte(user.Name), encoded)
	})
}

// SetUser changes an existing user.
func (s *BoltUserStore) SetUser(user User) error {
	return s.Update(func(tx *bbolt.Tx) error {
		users := tx.Bucket([]byte(boltUserBucketName))
		if users.Get([]byte(user.Name)) == nil {
			return ErrUserNotFound
		}

		encoded, err := user.Encode()
		if err != nil {
			return err
		}

		return users.Put([]byte(user.Name), encoded)
	})
}

// RemoveUser removes a user of given username.
func (s *BoltUserStore) RemoveUser(name string) error {
	return s.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(boltUserBucketName)).Delete([]byte(name))
	})
}

// SingleUserStore implements UserStore while enforcing only having a single user.
type SingleUserStore struct {
	UserStore
	username string
}

// NewSingleUserStore creates a new SingleUserStore with provided username and UserStore.
func NewSingleUserStore(username string, users UserStore) *SingleUserStore {
	return &SingleUserStore{
		UserStore: users,
		username:  username,
	}
}

// User gets a user.
func (s *SingleUserStore) User(name string) (*User, error) {
	if !s.isNameAllowed(name) {
		return nil, ErrNameNotAllowed
	}

	return s.UserStore.User(name)
}

// AddUser adds a new user.
func (s *SingleUserStore) AddUser(user User) error {
	if !s.isNameAllowed(user.Name) {
		return ErrNameNotAllowed
	}

	return s.UserStore.AddUser(user)
}

// SetUser sets an existing user.
func (s *SingleUserStore) SetUser(user User) error {
	if !s.isNameAllowed(user.Name) {
		return ErrNameNotAllowed
	}

	return s.UserStore.SetUser(user)
}

// RemoveUser removes a user.
func (s *SingleUserStore) RemoveUser(name string) error {
	if !s.isNameAllowed(name) {
		return ErrNameNotAllowed
	}

	return s.UserStore.RemoveUser(name)
}

func (s *SingleUserStore) isNameAllowed(name string) bool {
	return name == s.username
}

func checkUsernameFormat(name string) bool {
	return regexp.MustCompile(`^[a-z0-9_-]{4,21}$`).MatchString(name)
}

func checkPasswordFormat(password string) error {
	if len(password) < minPasswordLen || len(password) > maxPasswordLen {
		return ErrBadPasswordLen
	}

	return checkPasswordStrength(password)
}

func checkPasswordStrength(password string) error {
	if len(password) == 0 {
		return ErrSimplePassword
	}

	passwordClasses := [][]*unicode.RangeTable{
		{unicode.Upper, unicode.Title},
		{unicode.Lower},
		{unicode.Number, unicode.Digit},
		{unicode.Space, unicode.Symbol, unicode.Punct, unicode.Mark},
	}

	seen := make([]bool, len(passwordClasses))

	for _, r := range password {
		if r < '!' || r > unicode.MaxASCII {
			return ErrNonASCII
		}

		for i, class := range passwordClasses {
			if unicode.IsOneOf(class, r) {
				seen[i] = true
			}
		}
	}

	for _, v := range seen {
		if !v {
			return ErrSimplePassword
		}
	}

	return nil
}
