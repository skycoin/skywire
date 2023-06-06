// Package boltdb contains code of the chat repo of interfaceadapters
package boltdb

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/boltdb/bolt"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
)

// VISORBUCKET defines the key for the visor bucket
const VISORBUCKET = "visors"

// VisorRepo Implements the Repository Interface to provide an in-memory storage provider
type VisorRepo struct {
	db       *bolt.DB
	visorsMu sync.Mutex
}

// NewVisorRepo Constructor
func NewVisorRepo() *VisorRepo {
	r := VisorRepo{}

	db, err := bolt.Open("chats.db", 0600, nil)
	if err != nil {
		fmt.Println(err.Error())
	}
	r.db = db

	return &r
}

// GetByPK Returns the visor with the provided pk
func (r *VisorRepo) GetByPK(pk cipher.PubKey) (*chat.Visor, error) {
	r.visorsMu.Lock()
	defer r.visorsMu.Unlock()

	var vsr chat.Visor
	err := r.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(VISORBUCKET))
		if b == nil {
			// The bucket doesn't exist
			return fmt.Errorf("no visors in repository")
		}
		v := b.Get([]byte(pk.Hex()))

		err := json.Unmarshal([]byte(v), &vsr)
		if err != nil {
			return fmt.Errorf("could not get visor by pk %v", err)
		}
		return nil
	})
	return &vsr, err
}

// GetAll Returns all stored visors
func (r *VisorRepo) GetAll() ([]chat.Visor, error) {
	r.visorsMu.Lock()
	defer r.visorsMu.Unlock()

	var vsrs []chat.Visor

	err := r.db.View(func(tx *bolt.Tx) error {
		// Assume bucket exists and has keys
		b := tx.Bucket([]byte(VISORBUCKET))
		if b == nil {
			// The bucket doesn't exist
			return fmt.Errorf("no visors in repository")
		}

		// Create a cursor to iterate over the keys in the bucket
		c := b.Cursor()
		key, _ := c.First()

		// Check if the first key is nil
		if key == nil {
			return fmt.Errorf("no visors in repository")
		}
		err := b.ForEach(func(k, v []byte) error {
			var vsr chat.Visor
			err := json.Unmarshal([]byte(v), &vsr)
			if err != nil {
				return fmt.Errorf("could not get visor: %v", err)
			}
			vsrs = append(vsrs, vsr)
			return nil
		})
		return err

	})
	if err != nil {
		fmt.Println(err.Error())
	}
	return vsrs, nil
	//TODO: return err and handle correctly in caller functions? -> BUT we don't want to have an error when getAll is called, only an empty slice
}

// Add adds the provided visor to the repository
func (r *VisorRepo) Add(visor chat.Visor) error {
	return r.Set(visor)
}

// Set sets the provided visor
func (r *VisorRepo) Set(visor chat.Visor) error {
	r.visorsMu.Lock()
	defer r.visorsMu.Unlock()

	// Store the user model in the user bucket using the pk as the key.
	err := r.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(VISORBUCKET))
		if err != nil {
			return err
		}

		encoded, err := json.Marshal(visor)
		if err != nil {
			return err
		}
		return b.Put([]byte(visor.GetPK().Hex()), encoded)
	})
	return err
}

// Delete deletes the chat with the provided pk
func (r *VisorRepo) Delete(pk cipher.PubKey) error {
	r.visorsMu.Lock()
	defer r.visorsMu.Unlock()

	err := r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(VISORBUCKET))
		if b == nil {
			// The bucket doesn't exist
			return fmt.Errorf("no visors in repository")
		}
		return b.Delete([]byte(pk.Hex()))
	})
	return err
}

// Close closes the db
func (r *VisorRepo) Close() error {
	return r.db.Close()
}
