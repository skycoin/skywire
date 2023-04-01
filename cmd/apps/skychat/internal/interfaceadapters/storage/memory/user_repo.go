// Package memory contains code of the user repo of interfaceadapters
package memory

import (
	"fmt"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
)

// UserRepo Implements the Repository Interface to provide an in-memory storage provider
type UserRepo struct {
	user user.User
}

// NewUserRepo Constructor
func NewUserRepo(pk cipher.PubKey) *UserRepo {
	r := UserRepo{}

	var err error
	err = r.NewUser()
	r.user.GetInfo().SetPK(pk)
	if err != nil {
		fmt.Println(err)
	}

	return &r
}

// NewUser fills repo with a new user, if none has been set
// also returns a user when a user has been set already
func (r *UserRepo) NewUser() error {
	if !r.user.IsEmpty() {
		return fmt.Errorf("user already defined")
	}
	err := r.SetUser(user.NewDefaultUser())
	if err != nil {
		return err
	}
	return nil
}

// GetUser returns the user
func (r *UserRepo) GetUser() (*user.User, error) {
	if r.user.IsEmpty() {
		return nil, fmt.Errorf("user not found")
	}
	return &r.user, nil
}

// SetUser updates the provided user
func (r *UserRepo) SetUser(user *user.User) error {
	r.user = *user
	return nil
}
