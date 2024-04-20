// Package boltdb contains code of the user repo of interfaceadapters
package boltdb

import (
	"encoding/json"

	"github.com/boltdb/bolt"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
)

// USERBUCKET defines the key for the users bucket
const USERBUCKET = "users"

// UserRepo Implements the Repository Interface to provide an in-memory storage provider
type UserRepo struct {
	pk  cipher.PubKey
	db  *bolt.DB
	log *logging.Logger
}

// NewUserRepo Constructor
func NewUserRepo(pk cipher.PubKey) *UserRepo {
	r := UserRepo{}

	r.log = logging.MustGetLogger("chat:user-repo")

	db, err := bolt.Open("user.db", 0600, nil)
	if err != nil {
		r.log.Errorln(err)
	}
	r.db = db

	r.pk = pk

	err = r.NewUser()
	if err != nil {
		r.log.Errorln(err)
	}

	return &r
}

// NewUser fills repo with a new user, if none has been set
func (r *UserRepo) NewUser() error {
	// Store the user model in the user bucket using the pk as the key.
	err := r.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(USERBUCKET))
		if err != nil {
			return err
		}

		//check if the key is already available, then we don't have to make a new user
		ok := b.Get([]byte(r.pk.Hex()))
		if ok != nil {
			return nil
		}

		//make new default user and set pk
		usr := user.NewDefaultUser()
		usr.GetInfo().SetPK(r.pk)

		encoded, err := json.Marshal(usr)
		if err != nil {
			return err
		}
		return b.Put([]byte(r.pk.Hex()), encoded)
	})
	return err
}

// GetUser returns the user
func (r *UserRepo) GetUser() (*user.User, error) {
	var usr user.User
	err := r.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(USERBUCKET))
		v := b.Get([]byte(r.pk.Hex()))
		return json.Unmarshal(v, &usr)
	})
	return &usr, err
}

// SetUser updates the provided user
func (r *UserRepo) SetUser(user *user.User) error {
	// Store the user model in the user bucket using the username as the key.
	err := r.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(USERBUCKET))
		if err != nil {
			return err
		}

		encoded, err := json.Marshal(user)
		if err != nil {
			return err
		}
		return b.Put([]byte(user.GetInfo().GetPK().Hex()), encoded)
	})
	return err
}

// Close closes the db
func (r *UserRepo) Close() error {
	return r.db.Close()
}
